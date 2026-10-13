// Package samltest constructs local SAML fixtures with synthetic signing keys.
// It must only be imported by tests. It never contacts an identity provider.
package samltest

import (
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha512" // Register the SHA-384 and SHA-512 fixture signing algorithms.
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/beevik/etree"
	"github.com/google/uuid"
	dsig "github.com/russellhaering/goxmldsig"
	"github.com/russellhaering/goxmldsig/etreeutils"
)

const (
	AssertionNamespace = "urn:oasis:names:tc:SAML:2.0:assertion"
	ProtocolNamespace  = "urn:oasis:names:tc:SAML:2.0:protocol"
	PersistentNameID   = "urn:oasis:names:tc:SAML:2.0:nameid-format:persistent"
)

// Fixture owns an ephemeral RSA key and certificate. Mutations passed to
// SignedResponse run before signing, allowing independently valid signatures on
// semantically invalid assertions. To test tampering, mutate the returned XML.
type Fixture struct {
	Config                           auth.SAMLSPConfig
	AssertionID, ResponseID, Subject string
	Now                              time.Time
	SignatureMethod                  string
	t                                testing.TB
	key                              *rsa.PrivateKey
	certDER                          []byte
}

func NewFixture(t testing.TB) *Fixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Synthetic local SAML test IdP"},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &Fixture{
		Config: auth.SAMLSPConfig{
			AccountID: 41, ConfigVersion: 7,
			IDPEntityID:    "https://idp.saml.test/entity",
			SPEntityID:     "https://chat.saml.test/saml/metadata",
			SSOURL:         "https://idp.saml.test/sso",
			ACSURL:         "https://chat.saml.test/saml/acs",
			CertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		},
		AssertionID: "_assertion_" + uuid.NewString(),
		ResponseID:  "_response_" + uuid.NewString(),
		Subject:     "persistent-local-user-42", Now: now,
		SignatureMethod: dsig.RSASHA256SignatureMethod,
		t:               t, key: key, certDER: der,
	}
}

// Response builds a standards-shaped but unsigned Response. It is never an
// authentication capability until the production verifier accepts a signature.
func (f *Fixture) Response(requestID string) *etree.Element {
	response := etree.NewElement("samlp:Response")
	response.CreateAttr("xmlns:samlp", ProtocolNamespace)
	response.CreateAttr("xmlns:saml", AssertionNamespace)
	response.CreateAttr("ID", f.ResponseID)
	response.CreateAttr("Version", "2.0")
	response.CreateAttr("IssueInstant", f.Now.Format(time.RFC3339Nano))
	response.CreateAttr("Destination", f.Config.ACSURL)
	response.CreateAttr("InResponseTo", requestID)
	response.CreateElement("saml:Issuer").SetText(f.Config.IDPEntityID)
	status := response.CreateElement("samlp:Status")
	status.CreateElement("samlp:StatusCode").CreateAttr("Value", "urn:oasis:names:tc:SAML:2.0:status:Success")
	assertion := response.CreateElement("saml:Assertion")
	assertion.CreateAttr("xmlns:saml", AssertionNamespace)
	assertion.CreateAttr("ID", f.AssertionID)
	assertion.CreateAttr("Version", "2.0")
	assertion.CreateAttr("IssueInstant", f.Now.Format(time.RFC3339Nano))
	assertion.CreateElement("saml:Issuer").SetText(f.Config.IDPEntityID)
	subject := assertion.CreateElement("saml:Subject")
	nameID := subject.CreateElement("saml:NameID")
	nameID.CreateAttr("Format", PersistentNameID)
	nameID.CreateAttr("NameQualifier", f.Config.IDPEntityID)
	nameID.CreateAttr("SPNameQualifier", f.Config.SPEntityID)
	nameID.SetText(f.Subject)
	confirmation := subject.CreateElement("saml:SubjectConfirmation")
	confirmation.CreateAttr("Method", "urn:oasis:names:tc:SAML:2.0:cm:bearer")
	data := confirmation.CreateElement("saml:SubjectConfirmationData")
	data.CreateAttr("Recipient", f.Config.ACSURL)
	data.CreateAttr("InResponseTo", requestID)
	data.CreateAttr("NotOnOrAfter", f.Now.Add(2*time.Minute).Format(time.RFC3339Nano))
	conditions := assertion.CreateElement("saml:Conditions")
	conditions.CreateAttr("NotBefore", f.Now.Add(-10*time.Second).Format(time.RFC3339Nano))
	conditions.CreateAttr("NotOnOrAfter", f.Now.Add(2*time.Minute).Format(time.RFC3339Nano))
	conditions.CreateElement("saml:AudienceRestriction").CreateElement("saml:Audience").SetText(f.Config.SPEntityID)
	authn := assertion.CreateElement("saml:AuthnStatement")
	authn.CreateAttr("AuthnInstant", f.Now.Add(-10*time.Second).Format(time.RFC3339Nano))
	authn.CreateAttr("SessionIndex", "_session_local")
	authn.CreateAttr("SessionNotOnOrAfter", f.Now.Add(2*time.Minute).Format(time.RFC3339Nano))
	authn.CreateElement("saml:AuthnContext").CreateElement("saml:AuthnContextClassRef").SetText("urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport")
	return response
}

func (f *Fixture) SignedResponse(requestID string, mutate ...func(*etree.Element)) []byte {
	f.t.Helper()
	response := f.Response(requestID)
	for _, change := range mutate {
		change(response)
	}
	assertion := response.SelectElement("saml:Assertion")
	if assertion == nil {
		f.t.Fatal("fixture requires an assertion to sign")
	}
	signed := f.SignElement(assertion)
	response.RemoveChild(assertion)
	response.AddChild(signed)
	return XML(f.t, response)
}

// SignElement uses the maintained goxmldsig signer, rather than implementing XML
// canonicalization, digesting, or signature generation in fixture code.
func (f *Fixture) SignElement(element *etree.Element) *etree.Element {
	f.t.Helper()
	ctx, err := dsig.NewSigningContext(f.key, [][]byte{f.certDER})
	if err != nil {
		f.t.Fatal(err)
	}
	ctx.IdAttribute = "ID"
	ctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	if err := ctx.SetSignatureMethod(f.SignatureMethod); err != nil {
		f.t.Fatal(err)
	}
	signed, err := ctx.SignEnveloped(element)
	if err != nil {
		f.t.Fatal(err)
	}
	signature := signed.SelectElement("ds:Signature")
	if signature == nil {
		f.t.Fatal("signer did not generate a signature")
	}
	// goxmldsig appends the new signature token without assigning its etree
	// parent/index. Remove by the actual child slot before placing it in SAML's
	// schema order; RemoveChild(signature) would leave the original token behind.
	for index, child := range signed.Child {
		if child == signature {
			signed.RemoveChildAt(index)
			break
		}
	}
	signed.InsertChildAt(1, signature)
	return signed
}

// ReSignSignedInfo uses the maintained canonicalizer and signer to authenticate
// a deliberately modified SignedInfo. This enables tests of cryptographically
// valid but unsupported signature profiles, rather than only corrupt bytes.
func (f *Fixture) ReSignSignedInfo(signature *etree.Element) {
	f.t.Helper()
	ctx, err := dsig.NewSigningContext(f.key, [][]byte{f.certDER})
	if err != nil {
		f.t.Fatal(err)
	}
	ctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	if err := ctx.SetSignatureMethod(f.SignatureMethod); err != nil {
		f.t.Fatal(err)
	}
	info := signature.SelectElement("ds:SignedInfo")
	if info == nil {
		f.t.Fatal("missing SignedInfo")
	}
	parentContext, err := etreeutils.NSBuildParentContext(info)
	if err != nil {
		f.t.Fatal(err)
	}
	detached, err := etreeutils.NSDetatch(parentContext, info)
	if err != nil {
		f.t.Fatal(err)
	}
	canonical, err := ctx.Canonicalizer.Canonicalize(detached)
	if err != nil {
		f.t.Fatal(err)
	}
	signed, err := ctx.SignString(string(canonical))
	if err != nil {
		f.t.Fatal(err)
	}
	signature.SelectElement("ds:SignatureValue").SetText(base64.StdEncoding.EncodeToString(signed))
}

func XML(t testing.TB, root *etree.Element) []byte {
	t.Helper()
	doc := etree.NewDocument()
	doc.SetRoot(root)
	raw, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func Parse(t testing.TB, raw []byte) *etree.Element {
	t.Helper()
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(raw); err != nil {
		t.Fatal(err)
	}
	return doc.Root()
}
