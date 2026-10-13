package auth_test

import (
	"bytes"
	"compress/flate"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/auth/samltest"
	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

const samlRequestID = "_requested_local_login_1234567890"
const samlRelayState = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func mustSAMLProvider(t testing.TB, config auth.SAMLSPConfig) *auth.SAMLServiceProvider {
	t.Helper()
	provider, err := auth.NewSAMLServiceProvider(config)
	if err != nil {
		t.Fatalf("construct SAML provider: %v", err)
	}
	return provider
}

func samlAt(root *etree.Element, path string) *etree.Element {
	value := root.FindElement(path)
	if value == nil {
		panic("fixture path missing: " + path)
	}
	return value
}

func samlRemove(root *etree.Element, path string) {
	element := samlAt(root, path)
	element.Parent().RemoveChild(element)
}

func samlDuplicate(root *etree.Element, path string) {
	element := samlAt(root, path)
	element.Parent().AddChild(element.Copy())
}

func TestSAMLVerifierAcceptsOnlyTrustedSignedAssertion(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	for _, method := range []string{dsig.RSASHA256SignatureMethod, dsig.RSASHA384SignatureMethod, dsig.RSASHA512SignatureMethod} {
		t.Run(method[strings.LastIndex(method, "#")+1:], func(t *testing.T) {
			fixture.SignatureMethod = method
			verified, err := provider.VerifyXML(fixture.SignedResponse(samlRequestID), samlRequestID)
			if err != nil {
				t.Fatalf("real signed fixture rejected: %v", err)
			}
			if verified == nil {
				t.Fatal("missing verified capability")
			}
			if verified.AssertionID() != fixture.AssertionID || verified.ResponseID() != fixture.ResponseID || verified.RequestID() != samlRequestID {
				t.Fatal("verified identifiers differ from signed fixture")
			}
			if verified.Issuer() != fixture.Config.IDPEntityID || verified.Subject() != fixture.Subject {
				t.Fatal("verified principal differs from signed fixture")
			}
			if verified.NameIDFormat() != samltest.PersistentNameID || verified.NameQualifier() != fixture.Config.IDPEntityID || verified.SPNameQualifier() != fixture.Config.SPEntityID {
				t.Fatal("verified principal qualifiers differ")
			}
			if verified.AccountID() != fixture.Config.AccountID || verified.ConfigVersion() != fixture.Config.ConfigVersion || verified.ConfigDigest() != provider.ConfigDigest() {
				t.Fatal("verified capability is not bound to account and configuration")
			}
			if !verified.ExpiresAt().Equal(fixture.Now.Add(2 * time.Minute)) {
				t.Fatalf("unexpected capability expiry %v", verified.ExpiresAt())
			}
		})
	}
}

func TestSAMLVerifierBindsEarliestSignedExpiry(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	for _, path := range []string{"saml:Assertion/saml:Conditions", "saml:Assertion/saml:Subject/saml:SubjectConfirmation/saml:SubjectConfirmationData"} {
		t.Run(path, func(t *testing.T) {
			expires := fixture.Now.Add(40 * time.Second)
			raw := fixture.SignedResponse(samlRequestID, func(root *etree.Element) {
				samlAt(root, path).CreateAttr("NotOnOrAfter", expires.Format(time.RFC3339Nano))
				if path == "saml:Assertion/saml:Conditions" {
					samlAt(root, "saml:Assertion/saml:Subject/saml:SubjectConfirmation/saml:SubjectConfirmationData").CreateAttr("NotOnOrAfter", expires.Format(time.RFC3339Nano))
				}
			})
			verified, err := provider.VerifyXML(raw, samlRequestID)
			if err != nil {
				t.Fatal(err)
			}
			if !verified.ExpiresAt().Equal(expires) {
				t.Fatal("capability outlives its earliest signed expiry")
			}
		})
	}
	t.Run("signed_session_expiry", func(t *testing.T) {
		expires := fixture.Now.Add(30 * time.Second)
		raw := fixture.SignedResponse(samlRequestID, func(root *etree.Element) {
			samlAt(root, "saml:Assertion/saml:AuthnStatement").CreateAttr("SessionNotOnOrAfter", expires.Format(time.RFC3339Nano))
		})
		verified, err := provider.VerifyXML(raw, samlRequestID)
		if err != nil {
			t.Fatal(err)
		}
		if !verified.ExpiresAt().Equal(expires) {
			t.Fatal("capability outlives the signed session")
		}
	})
}

func TestSAMLVerifierRejectsCorrectlySignedInvalidSemantics(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	assertion := "saml:Assertion"
	conditions := assertion + "/saml:Conditions"
	confirmation := assertion + "/saml:Subject/saml:SubjectConfirmation"
	data := confirmation + "/saml:SubjectConfirmationData"
	nameID := assertion + "/saml:Subject/saml:NameID"
	attr := func(path, key, value string) func(*etree.Element) {
		return func(root *etree.Element) {
			if path == "" {
				root.CreateAttr(key, value)
			} else {
				samlAt(root, path).CreateAttr(key, value)
			}
		}
	}
	text := func(path, value string) func(*etree.Element) {
		return func(root *etree.Element) { samlAt(root, path).SetText(value) }
	}
	remove := func(path string) func(*etree.Element) { return func(root *etree.Element) { samlRemove(root, path) } }
	duplicate := func(path string) func(*etree.Element) { return func(root *etree.Element) { samlDuplicate(root, path) } }
	stamp := func(offset time.Duration) string { return fixture.Now.Add(offset).Format(time.RFC3339Nano) }
	cases := []struct {
		name   string
		mutate func(*etree.Element)
	}{
		{"wrong_response_issuer", text("saml:Issuer", "https://attacker.invalid")},
		{"wrong_assertion_issuer", text(assertion+"/saml:Issuer", "https://attacker.invalid")},
		{"missing_response_issuer", remove("saml:Issuer")},
		{"missing_assertion_issuer", remove(assertion + "/saml:Issuer")},
		{"duplicate_response_issuer", duplicate("saml:Issuer")},
		{"duplicate_assertion_issuer", duplicate(assertion + "/saml:Issuer")},
		{"wrong_destination", attr("", "Destination", "https://attacker.invalid/acs")},
		{"missing_destination", func(root *etree.Element) { root.RemoveAttr("Destination") }},
		{"wrong_response_request", attr("", "InResponseTo", "_other_request")},
		{"missing_response_request", func(root *etree.Element) { root.RemoveAttr("InResponseTo") }},
		{"wrong_confirmation_request", attr(data, "InResponseTo", "_other_request")},
		{"missing_confirmation_request", func(root *etree.Element) { samlAt(root, data).RemoveAttr("InResponseTo") }},
		{"wrong_recipient", attr(data, "Recipient", "https://attacker.invalid/acs")},
		{"missing_recipient", func(root *etree.Element) { samlAt(root, data).RemoveAttr("Recipient") }},
		{"wrong_audience", text(conditions+"/saml:AudienceRestriction/saml:Audience", "https://other-service.invalid")},
		{"missing_audience", remove(conditions + "/saml:AudienceRestriction/saml:Audience")},
		{"multiple_audiences", duplicate(conditions + "/saml:AudienceRestriction/saml:Audience")},
		{"missing_restriction", remove(conditions + "/saml:AudienceRestriction")},
		{"multiple_restrictions", duplicate(conditions + "/saml:AudienceRestriction")},
		{"duplicate_conditions", duplicate(conditions)},
		{"missing_conditions", remove(conditions)},
		{"duplicate_subject", duplicate(assertion + "/saml:Subject")},
		{"missing_subject", remove(assertion + "/saml:Subject")},
		{"empty_name_id", text(nameID, "")},
		{"whitespace_name_id", text(nameID, "   ")},
		{"duplicate_name_id", duplicate(nameID)},
		{"missing_name_id", remove(nameID)},
		{"email_name_id", attr(nameID, "Format", "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress")},
		{"transient_name_id", attr(nameID, "Format", "urn:oasis:names:tc:SAML:2.0:nameid-format:transient")},
		{"wrong_name_qualifier", attr(nameID, "NameQualifier", "https://other-idp.invalid")},
		{"wrong_sp_name_qualifier", attr(nameID, "SPNameQualifier", "https://other-sp.invalid")},
		{"wrong_confirmation_method", attr(confirmation, "Method", "urn:oasis:names:tc:SAML:2.0:cm:holder-of-key")},
		{"missing_confirmation", remove(confirmation)},
		{"multiple_confirmations", duplicate(confirmation)},
		{"multiple_confirmation_data", duplicate(data)},
		{"missing_confirmation_data", remove(data)},
		{"expired_conditions", attr(conditions, "NotOnOrAfter", stamp(-time.Second))},
		{"expired_confirmation", attr(data, "NotOnOrAfter", stamp(-time.Second))},
		{"missing_conditions_start", func(root *etree.Element) { samlAt(root, conditions).RemoveAttr("NotBefore") }},
		{"missing_conditions_expiry", func(root *etree.Element) { samlAt(root, conditions).RemoveAttr("NotOnOrAfter") }},
		{"missing_confirmation_expiry", func(root *etree.Element) { samlAt(root, data).RemoveAttr("NotOnOrAfter") }},
		{"future_conditions", attr(conditions, "NotBefore", stamp(time.Minute))},
		{"excessive_conditions_lifetime", attr(conditions, "NotOnOrAfter", stamp(6*time.Minute))},
		{"invalid_conditions_time", attr(conditions, "NotBefore", "not-a-time")},
		{"stale_response", attr("", "IssueInstant", stamp(-2*time.Minute))},
		{"stale_assertion", attr(assertion, "IssueInstant", stamp(-2*time.Minute))},
		{"future_response", attr("", "IssueInstant", stamp(time.Minute))},
		{"future_assertion", attr(assertion, "IssueInstant", stamp(time.Minute))},
		{"missing_response_instant", func(root *etree.Element) { root.RemoveAttr("IssueInstant") }},
		{"missing_assertion_instant", func(root *etree.Element) { samlAt(root, assertion).RemoveAttr("IssueInstant") }},
		{"invalid_response_instant", attr("", "IssueInstant", "not-a-time")},
		{"invalid_assertion_instant", attr(assertion, "IssueInstant", "not-a-time")},
		{"missing_authn_statement", remove(assertion + "/saml:AuthnStatement")},
		{"duplicate_authn_statement", duplicate(assertion + "/saml:AuthnStatement")},
		{"stale_authn_statement", attr(assertion+"/saml:AuthnStatement", "AuthnInstant", stamp(-10*time.Minute))},
		{"future_authn_statement", attr(assertion+"/saml:AuthnStatement", "AuthnInstant", stamp(time.Minute))},
		{"expired_session", attr(assertion+"/saml:AuthnStatement", "SessionNotOnOrAfter", stamp(-time.Second))},
		{"wrong_response_version", attr("", "Version", "1.1")},
		{"wrong_assertion_version", attr(assertion, "Version", "1.1")},
		{"missing_response_id", func(root *etree.Element) { root.RemoveAttr("ID") }},
		{"same_response_assertion_id", attr("", "ID", fixture.AssertionID)},
		{"failure_status", attr("samlp:Status/samlp:StatusCode", "Value", "urn:oasis:names:tc:SAML:2.0:status:Responder")},
		{"missing_status", remove("samlp:Status")},
		{"duplicate_status", duplicate("samlp:Status")},
		{"duplicate_status_code", duplicate("samlp:Status/samlp:StatusCode")},
		{"wrong_issuer_namespace", func(root *etree.Element) {
			samlAt(root, assertion+"/saml:Issuer").CreateAttr("xmlns:saml", "urn:attacker")
		}},
		{"wrong_audience_namespace", func(root *etree.Element) {
			samlAt(root, conditions+"/saml:AudienceRestriction/saml:Audience").CreateAttr("xmlns:saml", "urn:attacker")
		}},
		{"wrong_name_id_namespace", func(root *etree.Element) { samlAt(root, nameID).CreateAttr("xmlns:saml", "urn:attacker") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw := fixture.SignedResponse(samlRequestID, test.mutate)
			if got, err := provider.VerifyXML(raw, samlRequestID); err == nil || got != nil {
				t.Fatalf("invalid signed assertion produced capability: result=%v err=%v", got, err)
			}
		})
	}
	if got, err := provider.VerifyXML(fixture.SignedResponse(samlRequestID), ""); err == nil || got != nil {
		t.Fatal("empty expected request ID accepted")
	}
	if got, err := provider.VerifyXML(fixture.SignedResponse(samlRequestID), "_unrelated_request"); err == nil || got != nil {
		t.Fatal("uncorrelated response accepted")
	}
}

func TestSAMLVerifierRejectsSignatureAndWrappingAttacks(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	valid := fixture.SignedResponse(samlRequestID)
	sig := "saml:Assertion/ds:Signature"
	ref := sig + "/ds:SignedInfo/ds:Reference"
	cases := []struct {
		name   string
		mutate func(*etree.Element)
	}{
		{"tampered_subject", func(root *etree.Element) { samlAt(root, "saml:Assertion/saml:Subject/saml:NameID").SetText("attacker") }},
		{"tampered_audience", func(root *etree.Element) {
			samlAt(root, "saml:Assertion/saml:Conditions/saml:AudienceRestriction/saml:Audience").SetText("attacker")
		}},
		{"no_signature", func(root *etree.Element) { samlRemove(root, sig) }},
		{"duplicate_signature", func(root *etree.Element) { samlDuplicate(root, sig) }},
		{"missing_signature_value", func(root *etree.Element) { samlRemove(root, sig+"/ds:SignatureValue") }},
		{"empty_signature_value", func(root *etree.Element) { samlAt(root, sig+"/ds:SignatureValue").SetText("") }},
		{"bad_signature_value", func(root *etree.Element) { samlAt(root, sig+"/ds:SignatureValue").SetText("QUJD") }},
		{"duplicate_signature_value", func(root *etree.Element) { samlDuplicate(root, sig+"/ds:SignatureValue") }},
		{"duplicate_signed_info", func(root *etree.Element) { samlDuplicate(root, sig+"/ds:SignedInfo") }},
		{"external_reference", func(root *etree.Element) { samlAt(root, ref).CreateAttr("URI", "https://attacker.invalid/signed.xml") }},
		{"response_reference", func(root *etree.Element) { samlAt(root, ref).CreateAttr("URI", "#"+fixture.ResponseID) }},
		{"empty_reference", func(root *etree.Element) { samlAt(root, ref).CreateAttr("URI", "") }},
		{"missing_reference", func(root *etree.Element) { samlRemove(root, ref) }},
		{"duplicate_reference", func(root *etree.Element) { samlDuplicate(root, ref) }},
		{"unsupported_transform", func(root *etree.Element) {
			samlAt(root, ref+"/ds:Transforms/ds:Transform").CreateAttr("Algorithm", "http://www.w3.org/TR/1999/REC-xpath-19991116")
		}},
		{"missing_transforms", func(root *etree.Element) { samlRemove(root, ref+"/ds:Transforms") }},
		{"extra_transform", func(root *etree.Element) { samlDuplicate(root, ref+"/ds:Transforms/ds:Transform") }},
		{"unknown_canonicalization", func(root *etree.Element) {
			samlAt(root, sig+"/ds:SignedInfo/ds:CanonicalizationMethod").CreateAttr("Algorithm", "urn:unknown")
		}},
		{"unknown_signature_method", func(root *etree.Element) {
			samlAt(root, sig+"/ds:SignedInfo/ds:SignatureMethod").CreateAttr("Algorithm", "urn:unknown")
		}},
		{"mismatched_digest", func(root *etree.Element) {
			samlAt(root, ref+"/ds:DigestMethod").CreateAttr("Algorithm", "http://www.w3.org/2001/04/xmlenc#sha512")
		}},
		{"duplicate_digest", func(root *etree.Element) { samlDuplicate(root, ref+"/ds:DigestValue") }},
		{"wrong_signature_namespace", func(root *etree.Element) { samlAt(root, sig).CreateAttr("xmlns:ds", "urn:attacker") }},
		{"multiple_assertions", func(root *etree.Element) { samlDuplicate(root, "saml:Assertion") }},
		{"encrypted_assertion", func(root *etree.Element) {
			samlRemove(root, "saml:Assertion")
			root.CreateElement("saml:EncryptedAssertion")
		}},
		{"encrypted_plus_plaintext_assertion", func(root *etree.Element) { root.CreateElement("saml:EncryptedAssertion") }},
		{"nested_assertion_wrapper", func(root *etree.Element) {
			assertion := samlAt(root, "saml:Assertion")
			root.RemoveChild(assertion)
			root.CreateElement("samlp:Extensions").AddChild(assertion)
		}},
		{"duplicate_id_in_extension", func(root *etree.Element) {
			root.CreateElement("samlp:Extensions").CreateElement("Extra").CreateAttr("ID", fixture.AssertionID)
		}},
		{"response_signature_only", func(root *etree.Element) { samlRemove(root, sig); signed := fixture.SignElement(root); *root = *signed }},
		{"both_response_and_assertion_signed", func(root *etree.Element) { signed := fixture.SignElement(root); *root = *signed }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := samltest.Parse(t, valid)
			test.mutate(root)
			if got, err := provider.VerifyXML(samltest.XML(t, root), samlRequestID); err == nil || got != nil {
				t.Fatalf("signature/wrapping attack accepted: %v %v", got, err)
			}
		})
	}
	t.Run("correct_signature_from_untrusted_key", func(t *testing.T) {
		untrusted := samltest.NewFixture(t)
		if got, err := provider.VerifyXML(untrusted.SignedResponse(samlRequestID), samlRequestID); err == nil || got != nil {
			t.Fatal("untrusted signing key accepted")
		}
	})
	t.Run("cryptographically_valid_sha1", func(t *testing.T) {
		fixture.SignatureMethod = dsig.RSASHA1SignatureMethod
		if got, err := provider.VerifyXML(fixture.SignedResponse(samlRequestID), samlRequestID); err == nil || got != nil {
			t.Fatal("SHA-1 signature accepted")
		}
	})
}

func TestSAMLVerifierRejectsMalformedAndBoundedXML(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	valid := string(fixture.SignedResponse(samlRequestID))
	cases := map[string]string{
		"empty": "", "malformed": "<samlp:Response>", "non_xml": "eyJub3QiOiJ4bWwifQ==",
		"duplicate_root":                  valid + valid,
		"duplicate_attribute":             strings.Replace(valid, " Version=", ` ID="_duplicate" Version=`, 1),
		"duplicate_namespace":             strings.Replace(valid, " xmlns:samlp=", ` xmlns:samlp="urn:attacker" xmlns:samlp=`, 1),
		"undeclared_namespace":            strings.Replace(valid, `xmlns:samlp="`+samltest.ProtocolNamespace+`"`, "", 1),
		"dtd":                             `<!DOCTYPE samlp:Response [<!ENTITY hidden "attacker">]>` + valid,
		"external_dtd":                    `<!DOCTYPE samlp:Response SYSTEM "https://attacker.invalid/evil.dtd">` + valid,
		"processing_instruction":          `<?unsafe payload?>` + valid,
		"trailing_processing_instruction": valid + `<?unsafe payload?>`,
		"trailing_non_whitespace":         valid + "attacker",
		"oversized_xml":                   strings.Repeat(" ", 256*1024) + valid,
		"too_deep":                        strings.Repeat("<x>", 34) + valid + strings.Repeat("</x>", 34),
		"too_many_tokens":                 "<root>" + strings.Repeat("<x/>", 10001) + "</root>",
	}
	attributes := ""
	for i := 0; i < 34; i++ {
		attributes += fmt.Sprintf(` a%d="x"`, i)
	}
	cases["too_many_element_attributes"] = strings.Replace(valid, " Version=", attributes+" Version=", 1)
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := provider.VerifyXML([]byte(raw), samlRequestID); err == nil || got != nil {
				t.Fatalf("invalid XML accepted: %v %v", got, err)
			}
		})
	}
	if _, err := provider.VerifyXML([]byte(`<?xml version="1.0" encoding="UTF-8"?>`+valid), samlRequestID); err != nil {
		t.Fatalf("ordinary XML declaration rejected: %v", err)
	}
}

func TestSAMLVerifierRejectsValidMixedHashProfile(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	// Keep a genuine SHA-512 assertion digest, but sign SignedInfo with SHA-256.
	// The signature is valid XML-DSig; this application's narrower profile must
	// explicitly enforce matched digest/signature strengths.
	fixture.SignatureMethod = dsig.RSASHA512SignatureMethod
	root := samltest.Parse(t, fixture.SignedResponse(samlRequestID))
	signature := samlAt(root, "saml:Assertion/ds:Signature")
	samlAt(signature, "ds:SignedInfo/ds:SignatureMethod").CreateAttr("Algorithm", dsig.RSASHA256SignatureMethod)
	fixture.SignatureMethod = dsig.RSASHA256SignatureMethod
	fixture.ReSignSignedInfo(signature)
	block, _ := pem.Decode([]byte(fixture.Config.CertificatePEM))
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	check := dsig.NewDefaultValidationContext(&dsig.MemoryX509CertificateStore{Roots: []*x509.Certificate{certificate}})
	check.IdAttribute = "ID"
	if _, err := check.Validate(samlAt(root, "saml:Assertion")); err != nil {
		t.Fatalf("mixed-profile fixture is not cryptographically valid: %v", err)
	}
	if got, err := provider.VerifyXML(samltest.XML(t, root), samlRequestID); err == nil || got != nil {
		t.Fatal("valid but unsupported mixed-hash signature accepted")
	}
}

func TestSAMLPOSTBindingRejectsAmbiguousAndOversizedBodies(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	encoded := base64.StdEncoding.EncodeToString(fixture.SignedResponse(samlRequestID))
	valid := url.Values{"SAMLResponse": {encoded}, "RelayState": {samlRelayState}}.Encode()
	request := func(method, target, contentType, body string) *http.Request {
		r := httptest.NewRequest(method, target, strings.NewReader(body))
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		return r
	}
	verified, relay, err := provider.ParsePOST(request(http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid), samlRequestID)
	if err != nil || verified == nil || relay != samlRelayState {
		t.Fatalf("valid POST failed: verified=%v relay=%q err=%v", verified, relay, err)
	}
	cases := []struct{ name, method, target, contentType, body string }{
		{"get", http.MethodGet, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid},
		{"json", http.MethodPost, fixture.Config.ACSURL, "application/json", valid},
		{"missing_content_type", http.MethodPost, fixture.Config.ACSURL, "", valid},
		{"missing_response", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", "RelayState=x"},
		{"empty_response", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", "SAMLResponse=&RelayState=x"},
		{"invalid_base64", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", "SAMLResponse=***&RelayState=x"},
		{"malformed_form", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid + "&bad=%ZZ"},
		{"duplicate_response", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid + "&SAMLResponse=" + url.QueryEscape(encoded)},
		{"duplicate_relay", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid + "&RelayState=other"},
		{"artifact_alongside_response", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid + "&SAMLart=artifact"},
		{"artifact_only", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", "SAMLart=artifact&RelayState=x"},
		{"response_in_query", http.MethodPost, fixture.Config.ACSURL + "?SAMLResponse=" + url.QueryEscape(encoded), "application/x-www-form-urlencoded", valid},
		{"relay_in_query", http.MethodPost, fixture.Config.ACSURL + "?RelayState=other", "application/x-www-form-urlencoded", valid},
		{"oversized_body", http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid + "&padding=" + strings.Repeat("x", 1<<20)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, _, err := provider.ParsePOST(request(test.method, test.target, test.contentType, test.body), samlRequestID)
			if err == nil || got != nil {
				t.Fatalf("invalid POST returned capability: %v %v", got, err)
			}
		})
	}
	t.Run("preparsed_form_does_not_bypass_body_checks", func(t *testing.T) {
		r := request(http.MethodPost, fixture.Config.ACSURL, "application/x-www-form-urlencoded", valid+"&SAMLResponse="+url.QueryEscape(encoded))
		r.Form = url.Values{"SAMLResponse": {encoded}, "RelayState": {samlRelayState}}
		r.PostForm = r.Form
		if got, _, err := provider.ParsePOST(r, samlRequestID); err == nil || got != nil {
			t.Fatal("caller-populated form bypassed raw-body checks")
		}
	})
}

func TestSAMLPreparedRequestIsFreshAndBoundToConfiguration(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		prepared, err := provider.NewAuthenticationRequest()
		if err != nil {
			t.Fatal(err)
		}
		if len(prepared.ID()) < 20 || seen[prepared.ID()] {
			t.Fatal("request ID is short or reused")
		}
		seen[prepared.ID()] = true
		redirect, err := prepared.RedirectURL(samlRelayState)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(redirect)
		if err != nil {
			t.Fatal(err)
		}
		if u.Scheme+"://"+u.Host+u.Path != fixture.Config.SSOURL || u.Query().Get("RelayState") != samlRelayState {
			t.Fatal("redirect destination/state changed")
		}
		compressed, err := base64.StdEncoding.DecodeString(u.Query().Get("SAMLRequest"))
		if err != nil {
			t.Fatal(err)
		}
		reader := flate.NewReader(bytes.NewReader(compressed))
		raw, err := io.ReadAll(io.LimitReader(reader, 65537))
		reader.Close()
		if err != nil || len(raw) > 65536 {
			t.Fatalf("invalid compressed AuthnRequest: %v", err)
		}
		root := samltest.Parse(t, raw)
		if root.Tag != "AuthnRequest" || root.NamespaceURI() != samltest.ProtocolNamespace || root.SelectAttrValue("ID", "") != prepared.ID() || root.SelectAttrValue("Destination", "") != fixture.Config.SSOURL || root.SelectAttrValue("AssertionConsumerServiceURL", "") != fixture.Config.ACSURL {
			t.Fatal("prepared request is not bound to configured endpoint and fresh ID")
		}
		if root.SelectAttrValue("ForceAuthn", "") != "true" || root.SelectAttrValue("ProtocolBinding", "") != "urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" {
			t.Fatal("request does not require fresh authentication and POST binding")
		}
		for _, child := range root.ChildElements() {
			if child.Tag == "NameIDPolicy" && (child.SelectAttrValue("Format", "") != samltest.PersistentNameID || child.SelectAttrValue("AllowCreate", "") != "false") {
				t.Fatal("request permits an unrequested identity format or provisioning")
			}
		}
		issuer := root.FindElement("Issuer")
		if issuer == nil {
			for _, child := range root.ChildElements() {
				if child.Tag == "Issuer" {
					issuer = child
				}
			}
		}
		if issuer == nil || issuer.NamespaceURI() != samltest.AssertionNamespace || issuer.Text() != fixture.Config.SPEntityID {
			t.Fatal("request issuer differs from configured SP")
		}
		if _, err := prepared.RedirectURL(strings.Repeat("x", 81)); err == nil {
			t.Fatal("oversized RelayState accepted")
		}
	}
}

func TestSAMLProviderConfigIsValidatedAndDigestBound(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	if provider.ConfigDigest() == "" {
		t.Fatal("empty configuration digest")
	}
	versionOnly := fixture.Config
	versionOnly.ConfigVersion++
	if changed := mustSAMLProvider(t, versionOnly); changed.ConfigDigest() != provider.ConfigDigest() {
		t.Fatal("version counter unexpectedly changes the semantic configuration digest")
	}
	cases := []struct {
		name   string
		mutate func(*auth.SAMLSPConfig)
	}{
		{"zero_account", func(c *auth.SAMLSPConfig) { c.AccountID = 0 }},
		{"zero_version", func(c *auth.SAMLSPConfig) { c.ConfigVersion = 0 }},
		{"empty_issuer", func(c *auth.SAMLSPConfig) { c.IDPEntityID = "" }},
		{"empty_sp", func(c *auth.SAMLSPConfig) { c.SPEntityID = "" }},
		{"empty_sso", func(c *auth.SAMLSPConfig) { c.SSOURL = "" }},
		{"http_sso", func(c *auth.SAMLSPConfig) { c.SSOURL = "http://idp.saml.test/sso" }},
		{"http_acs", func(c *auth.SAMLSPConfig) { c.ACSURL = "http://chat.saml.test/acs" }},
		{"relative_acs", func(c *auth.SAMLSPConfig) { c.ACSURL = "/saml/acs" }},
		{"credentialed_sso", func(c *auth.SAMLSPConfig) { c.SSOURL = "https://user:password@idp.saml.test/sso" }},
		{"fragment_acs", func(c *auth.SAMLSPConfig) { c.ACSURL += "#fragment" }},
		{"empty_certificate", func(c *auth.SAMLSPConfig) { c.CertificatePEM = "" }},
		{"bad_certificate", func(c *auth.SAMLSPConfig) { c.CertificatePEM = "definitely not a certificate" }},
		{"certificate_prefixed_junk", func(c *auth.SAMLSPConfig) { c.CertificatePEM = "untrusted prefix\n" + c.CertificatePEM }},
		{"multiple_certificates", func(c *auth.SAMLSPConfig) { c.CertificatePEM += c.CertificatePEM }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			config := fixture.Config
			test.mutate(&config)
			if got, err := auth.NewSAMLServiceProvider(config); err == nil || got != nil {
				t.Fatal("invalid provider configuration accepted")
			}
		})
	}
	mutations := []func(*auth.SAMLSPConfig){
		func(c *auth.SAMLSPConfig) { c.AccountID++ }, func(c *auth.SAMLSPConfig) { c.IDPEntityID += "/other" }, func(c *auth.SAMLSPConfig) { c.SPEntityID += "/other" },
		func(c *auth.SAMLSPConfig) { c.SSOURL += "/other" }, func(c *auth.SAMLSPConfig) { c.ACSURL += "/other" },
	}
	for _, mutate := range mutations {
		config := fixture.Config
		mutate(&config)
		if changed := mustSAMLProvider(t, config); changed.ConfigDigest() == provider.ConfigDigest() {
			t.Fatal("configuration digest omitted a binding field")
		}
	}
	other := samltest.NewFixture(t)
	config := fixture.Config
	config.CertificatePEM = other.Config.CertificatePEM
	if changed := mustSAMLProvider(t, config); changed.ConfigDigest() == provider.ConfigDigest() {
		t.Fatal("configuration digest omitted trusted signing certificate")
	}
}

func TestSAMLProviderRejectsUnusableSigningCertificates(t *testing.T) {
	fixture := samltest.NewFixture(t)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	weakKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name          string
		key           crypto.Signer
		before, after time.Time
		usage         x509.KeyUsage
	}{
		{"expired", rsaKey, fixture.Now.Add(-time.Hour), fixture.Now.Add(-time.Minute), x509.KeyUsageDigitalSignature},
		{"not_yet_valid", rsaKey, fixture.Now.Add(time.Hour), fixture.Now.Add(2 * time.Hour), x509.KeyUsageDigitalSignature},
		{"weak_rsa", weakKey, fixture.Now.Add(-time.Hour), fixture.Now.Add(time.Hour), x509.KeyUsageDigitalSignature},
		{"unsupported_ec", ecKey, fixture.Now.Add(-time.Hour), fixture.Now.Add(time.Hour), x509.KeyUsageDigitalSignature},
		{"encryption_only", rsaKey, fixture.Now.Add(-time.Hour), fixture.Now.Add(time.Hour), x509.KeyUsageKeyEncipherment},
	}
	for i, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			template := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 1)), Subject: pkix.Name{CommonName: "Synthetic invalid certificate"}, NotBefore: test.before, NotAfter: test.after, KeyUsage: test.usage}
			der, err := x509.CreateCertificate(rand.Reader, template, template, test.key.Public(), test.key)
			if err != nil {
				t.Fatal(err)
			}
			config := fixture.Config
			config.CertificatePEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
			if got, err := auth.NewSAMLServiceProvider(config); err == nil || got != nil {
				t.Fatal("unusable signing certificate accepted")
			}
		})
	}
}

func TestSAMLZeroValuesCannotMintCapability(t *testing.T) {
	for _, provider := range []*auth.SAMLServiceProvider{nil, {}} {
		if got, err := provider.VerifyXML([]byte("<Response/>"), samlRequestID); err == nil || got != nil {
			t.Fatal("zero provider minted a capability")
		}
		if got, err := provider.NewAuthenticationRequest(); err == nil || got != nil {
			t.Fatal("zero provider minted an authentication request")
		}
		if provider.ConfigDigest() != "" {
			t.Fatal("zero provider has a configuration digest")
		}
	}
	for _, prepared := range []*auth.SAMLPreparedAuthnRequest{nil, {}} {
		if prepared.ID() != "" {
			t.Fatal("zero request has an ID")
		}
		if _, err := prepared.RedirectURL(samlRelayState); err == nil {
			t.Fatal("zero request redirects")
		}
	}
	for _, verified := range []*auth.SAMLVerifiedAssertion{nil, {}} {
		if verified.AssertionID() != "" || verified.ResponseID() != "" || verified.RequestID() != "" || verified.Issuer() != "" || verified.Subject() != "" || verified.NameIDFormat() != "" || verified.NameQualifier() != "" || verified.SPNameQualifier() != "" || verified.ConfigDigest() != "" || verified.AccountID() != 0 || verified.ConfigVersion() != 0 || !verified.ExpiresAt().IsZero() {
			t.Fatal("zero capability has authenticated claims")
		}
	}
}

func TestSAMLVerifierConcurrentReadOnlyUse(t *testing.T) {
	fixture := samltest.NewFixture(t)
	provider := mustSAMLProvider(t, fixture.Config)
	raw := fixture.SignedResponse(samlRequestID)
	var workers sync.WaitGroup
	errors := make(chan error, 32)
	for i := 0; i < 32; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			verified, err := provider.VerifyXML(raw, samlRequestID)
			if err != nil {
				errors <- err
				return
			}
			if verified.Subject() != fixture.Subject {
				errors <- fmt.Errorf("principal changed during concurrent verification")
			}
			prepared, err := provider.NewAuthenticationRequest()
			if err != nil {
				errors <- err
				return
			}
			if _, err = prepared.RedirectURL(samlRelayState); err != nil {
				errors <- err
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func FuzzSAMLVerifyXMLFailsClosed(f *testing.F) {
	fixture := samltest.NewFixture(f)
	provider := mustSAMLProvider(f, fixture.Config)
	valid := fixture.SignedResponse(samlRequestID)
	f.Add(valid)
	// Fuzz workers each generate a fresh synthetic key. Resolve this positive
	// seed locally so the successful verification path is covered in every
	// worker, without committing a private key or relying on shared test files.
	f.Add([]byte("local-valid-signed-fixture"))
	f.Add([]byte("<Response/>"))
	f.Add([]byte(`<!DOCTYPE x [<!ENTITY a "a">]><x>&a;</x>`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if bytes.Equal(raw, []byte("local-valid-signed-fixture")) {
			raw = valid
		}
		if len(raw) > 256*1024 {
			return
		}
		verified, err := provider.VerifyXML(raw, samlRequestID)
		if err != nil {
			if verified != nil {
				t.Fatal("error returned together with verified capability")
			}
			return
		}
		if verified == nil || verified.RequestID() != samlRequestID || verified.Issuer() != fixture.Config.IDPEntityID || verified.AccountID() != fixture.Config.AccountID || verified.ConfigDigest() != provider.ConfigDigest() {
			t.Fatal("verification returned an unbound capability")
		}
	})
}
