package auth

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type SAMLResponseXML struct {
	XMLName   xml.Name      `xml:"Response"`
	Issuer    string        `xml:"Issuer"`
	Assertion SAMLAssertion `xml:"Assertion"`
}

type SAMLAssertion struct {
	Issuer             string                 `xml:"Issuer"`
	Subject            SAMLSubject            `xml:"Subject"`
	Conditions         SAMLConditions         `xml:"Conditions"`
	AttributeStatement SAMLAttributeStatement `xml:"AttributeStatement"`
}

type SAMLSubject struct {
	NameID string `xml:"NameID"`
}

type SAMLConditions struct {
	NotBefore    string `xml:"NotBefore,attr"`
	NotOnOrAfter string `xml:"NotOnOrAfter,attr"`
}

type SAMLAttributeStatement struct {
	Attributes []SAMLAttribute `xml:"Attribute"`
}

type SAMLAttribute struct {
	Name   string   `xml:"Name,attr"`
	Values []string `xml:"AttributeValue"`
}

type SAMLUserClaims struct {
	Email     string
	FirstName string
	LastName  string
	Issuer    string
}

func stripXMLNamespaces(s string) string {
	re := regexp.MustCompile(`(</?)[a-zA-Z0-9_-]+:`)
	return re.ReplaceAllString(s, "$1")
}

// ParseSAMLResponse extracts UNTRUSTED XML data only. Never use its output to authenticate.
func ParseSAMLResponse(rawResponse string) (*SAMLUserClaims, error) {
	if rawResponse == "" {
		return nil, errors.New("empty SAML response")
	}

	xmlStr := rawResponse
	// If base64 encoded, decode it
	if decoded, err := base64.StdEncoding.DecodeString(rawResponse); err == nil && len(decoded) > 0 {
		xmlStr = string(decoded)
	}

	cleanedXML := stripXMLNamespaces(xmlStr)

	var resp SAMLResponseXML
	if err := xml.Unmarshal([]byte(cleanedXML), &resp); err != nil {
		// Fallback simple XML string parser if wrapped differently
		return parseSAMLFallback(cleanedXML)
	}

	email := resp.Assertion.Subject.NameID
	var firstName, lastName string
	for _, attr := range resp.Assertion.AttributeStatement.Attributes {
		nameLower := strings.ToLower(attr.Name)
		if strings.Contains(nameLower, "email") && len(attr.Values) > 0 {
			email = attr.Values[0]
		}
		if (strings.Contains(nameLower, "first_name") || strings.Contains(nameLower, "firstname") || strings.Contains(nameLower, "givenname")) && len(attr.Values) > 0 {
			firstName = attr.Values[0]
		}
		if (strings.Contains(nameLower, "last_name") || strings.Contains(nameLower, "lastname") || strings.Contains(nameLower, "surname")) && len(attr.Values) > 0 {
			lastName = attr.Values[0]
		}
	}

	if email == "" {
		return parseSAMLFallback(cleanedXML)
	}

	return &SAMLUserClaims{
		Email:     strings.TrimSpace(email),
		FirstName: firstName,
		LastName:  lastName,
		Issuer:    resp.Issuer,
	}, nil
}

func parseSAMLFallback(xmlContent string) (*SAMLUserClaims, error) {
	// Extract NameID tag if unmarshal failed due to namespaces
	startNameID := strings.Index(xmlContent, "<NameID")
	if startNameID == -1 {
		startNameID = strings.Index(xmlContent, "<saml:NameID")
	}
	if startNameID != -1 {
		closeTag := strings.Index(xmlContent[startNameID:], ">")
		if closeTag != -1 {
			endTag := strings.Index(xmlContent[startNameID+closeTag:], "</")
			if endTag != -1 {
				email := strings.TrimSpace(xmlContent[startNameID+closeTag+1 : startNameID+closeTag+endTag])
				if email != "" {
					return &SAMLUserClaims{Email: email}, nil
				}
			}
		}
	}

	// Try extracting email attribute
	if idx := strings.Index(xmlContent, "AttributeValue"); idx != -1 {
		closeTag := strings.Index(xmlContent[idx:], ">")
		if closeTag != -1 {
			endTag := strings.Index(xmlContent[idx+closeTag:], "</")
			if endTag != -1 {
				val := strings.TrimSpace(xmlContent[idx+closeTag+1 : idx+closeTag+endTag])
				if strings.Contains(val, "@") {
					return &SAMLUserClaims{Email: val}, nil
				}
			}
		}
	}

	return nil, errors.New("unable to extract valid identity from SAML payload")
}

// ErrSAMLVerificationUnavailable is returned until a complete, trusted SAML
// service provider verifies signatures, assertion references, audience, time,
// recipient, request correlation, and replay protection. XML parsing is not auth.
var ErrSAMLVerificationUnavailable = errors.New("SAML login unavailable: trusted assertion verification is not configured")

// VerifySAMLSignature deliberately fails closed. Substring extraction of
// SignedInfo does not implement XML DSig canonicalization or bind an assertion.
func VerifySAMLSignature(xmlContent string, certPEM string) (bool, error) {
	return false, ErrSAMLVerificationUnavailable
}

// GenerateMockGoogleIDToken returns a FORGED token for negative tests; it cannot authenticate.
func GenerateMockGoogleIDToken(email, name string) string {
	header := `{"alg":"RS256","typ":"JWT"}`
	payload, _ := json.Marshal(map[string]any{
		"email": email,
		"name":  name,
		"sub":   "google_user_12345",
		"iss":   "https://accounts.google.com",
		"exp":   time.Now().Add(2 * time.Hour).Unix(),
	})
	hdrEnc := base64.RawURLEncoding.EncodeToString([]byte(header))
	payEnc := base64.RawURLEncoding.EncodeToString(payload)
	sigEnc := base64.RawURLEncoding.EncodeToString([]byte("valid_mock_signature_bytes_12345"))
	return fmt.Sprintf("%s.%s.%s", hdrEnc, payEnc, sigEnc)
}

// GenerateMockSAMLResponse returns UNSIGNED XML for parsing/negative tests; it cannot authenticate.
func GenerateMockSAMLResponse(email, firstName, lastName string) string {
	xmlTemplate := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_resp123" Version="2.0" IssueInstant="%s">
  <saml:Issuer>https://idp.example.com</saml:Issuer>
  <saml:Assertion ID="_assert123" Version="2.0">
    <saml:Subject>
      <saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">%s</saml:NameID>
    </saml:Subject>
    <saml:AttributeStatement>
      <saml:Attribute Name="email"><saml:AttributeValue>%s</saml:AttributeValue></saml:Attribute>
      <saml:Attribute Name="first_name"><saml:AttributeValue>%s</saml:AttributeValue></saml:Attribute>
      <saml:Attribute Name="last_name"><saml:AttributeValue>%s</saml:AttributeValue></saml:Attribute>
    </saml:AttributeStatement>
  </saml:Assertion>
</samlp:Response>`, time.Now().Format(time.RFC3339), email, email, firstName, lastName)
	return base64.StdEncoding.EncodeToString([]byte(xmlTemplate))
}
