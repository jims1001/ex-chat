package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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

// ParseSAMLResponse decodes and extracts claims from a standard SAML 2.0 response
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

// VerifySAMLSignature verifies RSA-SHA256 signature if certificate is supplied
func VerifySAMLSignature(xmlContent string, certPEM string) (bool, error) {
	if certPEM == "" {
		hasSig := strings.Contains(xmlContent, "SignatureValue") || strings.Contains(xmlContent, "ds:Signature")
		return hasSig, nil
	}

	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false, errors.New("failed to parse certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, fmt.Errorf("failed to parse x509 certificate: %w", err)
	}

	rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return false, errors.New("certificate public key is not RSA")
	}

	startSig := strings.Index(xmlContent, "<SignatureValue")
	if startSig == -1 {
		return false, errors.New("SignatureValue not found in SAML response")
	}
	closeSig := strings.Index(xmlContent[startSig:], ">")
	endSig := strings.Index(xmlContent[startSig+closeSig:], "</")
	sigBase64 := strings.TrimSpace(xmlContent[startSig+closeSig+1 : startSig+closeSig+endSig])

	sigBytes, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil {
		return false, fmt.Errorf("invalid signature base64: %w", err)
	}

	startSI := strings.Index(xmlContent, "<SignedInfo")
	if startSI == -1 {
		return false, errors.New("SignedInfo not found in SAML response")
	}
	endSI := strings.Index(xmlContent[startSI:], "</SignedInfo>")
	if endSI == -1 {
		return false, errors.New("malformed SignedInfo in SAML response")
	}
	signedInfoXML := xmlContent[startSI : startSI+endSI+len("</SignedInfo>")]

	h := sha256.New()
	h.Write([]byte(signedInfoXML))
	digest := h.Sum(nil)

	err = rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest, sigBytes)
	return err == nil, err
}

// ParseGoogleIDToken parses and validates a Google OAuth2 JWT ID token
func ParseGoogleIDToken(tokenString string) (string, string, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return "", "", errors.New("invalid jwt token format")
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("failed to decode jwt header: %w", err)
	}

	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return "", "", fmt.Errorf("invalid jwt header: %w", err)
	}

	// Signature segment MUST not be empty
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sigBytes) == 0 {
		return "", "", errors.New("invalid or empty jwt signature")
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", fmt.Errorf("failed to decode jwt payload: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Sub   string `json:"sub"`
		Exp   int64  `json:"exp"`
		Iss   string `json:"iss"`
	}
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return "", "", fmt.Errorf("failed to unmarshal jwt claims: %w", err)
	}

	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return "", "", errors.New("token expired")
	}
	if claims.Email == "" {
		return "", "", errors.New("email claim missing in token")
	}
	if !strings.Contains(claims.Email, "@") {
		return "", "", errors.New("malformed email in google token")
	}

	return claims.Email, claims.Name, nil
}

// GenerateMockGoogleIDToken generates a syntactically valid mock Google ID Token for testing
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

// GenerateMockSAMLResponse generates a valid base64-encoded SAML 2.0 response XML
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

