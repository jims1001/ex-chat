package auth

// This file is an independently usable verification primitive, not an enabled
// login flow. The HTTP SAML handlers intentionally remain unavailable. A caller
// must correlate the browser and atomically consume durable request/replay state
// before treating a verified assertion as a login proof. No user mapping or
// session issuance is implemented here.
import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	_ "crypto/sha512" // Register permitted SHA-384/SHA-512 XML signature algorithms.
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
)

const (
	samlAssertionNS      = "urn:oasis:names:tc:SAML:2.0:assertion"
	samlProtocolNS       = "urn:oasis:names:tc:SAML:2.0:protocol"
	samlDSigNS           = "http://www.w3.org/2000/09/xmldsig#"
	samlPersistentNameID = "urn:oasis:names:tc:SAML:2.0:nameid-format:persistent"
	samlBearerMethod     = "urn:oasis:names:tc:SAML:2.0:cm:bearer"
	samlExclusiveC14N    = "http://www.w3.org/2001/10/xml-exc-c14n#"
	SAMLMaxXMLBytes      = 256 << 10
	SAMLMaxFormBytes     = 1 << 20
	SAMLMaxXMLTokens     = 10000
	SAMLMaxXMLDepth      = 32
	SAMLRequestTTL       = 5 * time.Minute
	samlClockSkew        = 30 * time.Second
	samlMaxIssueAge      = 90 * time.Second
)

var ErrInvalidSAMLProof = errors.New("invalid SAML proof")
var ErrInvalidSAMLConfiguration = errors.New("invalid SAML service provider configuration")
var samlIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)

// SAMLSPConfig is explicit trusted input. It must never be assembled from a
// callback's XML, Host header, or RelayState. Version is supplied by its future
// configuration owner; this primitive neither persists nor activates settings.
type SAMLSPConfig struct {
	AccountID      uint
	ConfigVersion  uint64
	IDPEntityID    string
	SPEntityID     string
	SSOURL         string
	ACSURL         string
	CertificatePEM string
}

type SAMLServiceProvider struct {
	sp          saml.ServiceProvider
	config      SAMLSPConfig
	certificate *x509.Certificate
	digest      string
}

// SAMLVerifiedAssertion is immutable outside auth. Its zero value is invalid.
// It is a cryptographic proof only, not authorization to map a user or issue a
// token. Identity is read exclusively from the library-verified assertion.
type SAMLVerifiedAssertion struct {
	assertionID, responseID, requestID, issuer, subject, nameIDFormat string
	nameQualifier, spNameQualifier, configDigest                      string
	accountID                                                         uint
	configVersion                                                     uint64
	expiresAt                                                         time.Time
}

func (v *SAMLVerifiedAssertion) AssertionID() string {
	if v == nil {
		return ""
	}
	return v.assertionID
}
func (v *SAMLVerifiedAssertion) ResponseID() string {
	if v == nil {
		return ""
	}
	return v.responseID
}
func (v *SAMLVerifiedAssertion) RequestID() string {
	if v == nil {
		return ""
	}
	return v.requestID
}
func (v *SAMLVerifiedAssertion) Issuer() string {
	if v == nil {
		return ""
	}
	return v.issuer
}
func (v *SAMLVerifiedAssertion) Subject() string {
	if v == nil {
		return ""
	}
	return v.subject
}
func (v *SAMLVerifiedAssertion) NameIDFormat() string {
	if v == nil {
		return ""
	}
	return v.nameIDFormat
}
func (v *SAMLVerifiedAssertion) NameQualifier() string {
	if v == nil {
		return ""
	}
	return v.nameQualifier
}
func (v *SAMLVerifiedAssertion) SPNameQualifier() string {
	if v == nil {
		return ""
	}
	return v.spNameQualifier
}
func (v *SAMLVerifiedAssertion) ConfigDigest() string {
	if v == nil {
		return ""
	}
	return v.configDigest
}
func (v *SAMLVerifiedAssertion) AccountID() uint {
	if v == nil {
		return 0
	}
	return v.accountID
}
func (v *SAMLVerifiedAssertion) ConfigVersion() uint64 {
	if v == nil {
		return 0
	}
	return v.configVersion
}
func (v *SAMLVerifiedAssertion) ExpiresAt() time.Time {
	if v == nil {
		return time.Time{}
	}
	return v.expiresAt
}

func NewSAMLServiceProvider(c SAMLSPConfig) (*SAMLServiceProvider, error) {
	if c.AccountID == 0 || c.ConfigVersion == 0 || !samlEntityID(c.IDPEntityID) || !samlEntityID(c.SPEntityID) || len(c.CertificatePEM) > 16384 {
		return nil, ErrInvalidSAMLConfiguration
	}
	sso, err := samlHTTPSURL(c.SSOURL)
	if err != nil {
		return nil, err
	}
	acs, err := samlHTTPSURL(c.ACSURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.TrimSpace(c.CertificatePEM), "-----BEGIN CERTIFICATE-----") {
		return nil, ErrInvalidSAMLConfiguration
	}
	block, rest := pem.Decode([]byte(c.CertificatePEM))
	if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, ErrInvalidSAMLConfiguration
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, ErrInvalidSAMLConfiguration
	}
	key, ok := cert.PublicKey.(*rsa.PublicKey)
	now := time.Now().UTC()
	if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 || key.E < 3 || key.E%2 == 0 || now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) || (cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0) {
		return nil, ErrInvalidSAMLConfiguration
	}
	certHash := sha256.Sum256(cert.Raw)
	digestInput, _ := json.Marshal(struct {
		AccountID                               uint
		IDP, SP, SSO, ACS, Certificate, Profile string
	}{c.AccountID, c.IDPEntityID, c.SPEntityID, sso.String(), acs.String(), hex.EncodeToString(certHash[:]), "signed-assertion-v1"})
	digest := sha256.Sum256(digestInput)
	metadata := &saml.EntityDescriptor{EntityID: c.IDPEntityID, IDPSSODescriptors: []saml.IDPSSODescriptor{{SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{ProtocolSupportEnumeration: samlProtocolNS, KeyDescriptors: []saml.KeyDescriptor{{Use: "signing", KeyInfo: saml.KeyInfo{X509Data: saml.X509Data{X509Certificates: []saml.X509Certificate{{Data: base64.StdEncoding.EncodeToString(cert.Raw)}}}}}}}}, SingleSignOnServices: []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: sso.String()}}}}}
	p := &SAMLServiceProvider{config: c, certificate: cert, digest: hex.EncodeToString(digest[:])}
	p.sp = saml.ServiceProvider{EntityID: c.SPEntityID, AcsURL: *acs, IDPMetadata: metadata, AuthnNameIDFormat: saml.NameIDFormat(samlPersistentNameID), AllowIDPInitiated: false}
	return p, nil
}
func (p *SAMLServiceProvider) ConfigDigest() string {
	if p == nil {
		return ""
	}
	return p.digest
}
func samlEntityID(s string) bool {
	if !boundedSAMLString(s, 1024) {
		return false
	}
	u, e := url.Parse(s)
	return e == nil && u.IsAbs() && u.User == nil && u.Fragment == ""
}
func samlHTTPSURL(s string) (*url.URL, error) {
	if !boundedSAMLString(s, 2048) {
		return nil, ErrInvalidSAMLConfiguration
	}
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Opaque != "" {
		return nil, ErrInvalidSAMLConfiguration
	}
	return u, nil
}
func boundedSAMLString(s string, n int) bool {
	if s == "" || len(s) > n || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

type SAMLPreparedAuthnRequest struct {
	request  *saml.AuthnRequest
	provider *SAMLServiceProvider
}

func (p *SAMLServiceProvider) NewAuthenticationRequest() (*SAMLPreparedAuthnRequest, error) {
	if p == nil || p.digest == "" || !time.Now().Before(p.certificate.NotAfter) {
		return nil, ErrInvalidSAMLConfiguration
	}
	r, e := p.sp.MakeAuthenticationRequest(p.config.SSOURL, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if e != nil {
		return nil, ErrInvalidSAMLProof
	}
	// The independent subset emits unsigned redirect requests. IdPs requiring
	// signed requests remain unsupported until explicit key configuration exists.
	allowCreate := false
	r.NameIDPolicy.AllowCreate = &allowCreate
	forceAuthn := true
	r.ForceAuthn = &forceAuthn
	return &SAMLPreparedAuthnRequest{request: r, provider: p}, nil
}
func (r *SAMLPreparedAuthnRequest) ID() string {
	if r == nil || r.request == nil {
		return ""
	}
	return r.request.ID
}
func (r *SAMLPreparedAuthnRequest) RedirectURL(relayState string) (string, error) {
	b, e := base64.RawURLEncoding.Strict().DecodeString(relayState)
	if r == nil || r.request == nil || e != nil || len(b) != 32 || len(relayState) != 43 {
		return "", ErrInvalidSAMLProof
	}
	u, e := r.request.Redirect(relayState, &r.provider.sp)
	if e != nil {
		return "", ErrInvalidSAMLProof
	}
	return u.String(), nil
}

// ParsePOST accepts only the bounded HTTP-POST response subset. Returned
// RelayState is UNTRUSTED until matched to the browser-bound durable request.
// No handler calls this function yet; SAML login remains HTTP 503.
func (p *SAMLServiceProvider) ParsePOST(r *http.Request, requestID string) (*SAMLVerifiedAssertion, string, error) {
	if p == nil || r == nil || r.Body == nil || r.Method != http.MethodPost || r.URL == nil || r.URL.RawQuery != "" || r.URL.Path != p.sp.AcsURL.Path || r.ContentLength > SAMLMaxFormBytes || (r.Header.Get("Content-Encoding") != "" && r.Header.Get("Content-Encoding") != "identity") {
		return nil, "", ErrInvalidSAMLProof
	}
	kind, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || kind != "application/x-www-form-urlencoded" || len(params) > 1 || (params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8")) {
		return nil, "", ErrInvalidSAMLProof
	}
	for k := range params {
		if k != "charset" {
			return nil, "", ErrInvalidSAMLProof
		}
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, SAMLMaxFormBytes+1))
	if e != nil || len(raw) > SAMLMaxFormBytes {
		return nil, "", ErrInvalidSAMLProof
	}
	form, e := url.ParseQuery(string(raw))
	if e != nil || len(form) != 2 || len(form["SAMLResponse"]) != 1 || len(form["RelayState"]) != 1 {
		return nil, "", ErrInvalidSAMLProof
	}
	relay := form.Get("RelayState")
	browserState, e := base64.RawURLEncoding.Strict().DecodeString(relay)
	if e != nil || len(browserState) != 32 || len(relay) != 43 {
		return nil, "", ErrInvalidSAMLProof
	}
	encoded := form.Get("SAMLResponse")
	if encoded == "" || len(encoded) > base64.StdEncoding.EncodedLen(SAMLMaxXMLBytes) {
		return nil, "", ErrInvalidSAMLProof
	}
	decoded, e := base64.StdEncoding.Strict().DecodeString(encoded)
	if e != nil {
		return nil, "", ErrInvalidSAMLProof
	}
	verified, e := p.VerifyXML(decoded, requestID)
	if e != nil {
		return nil, "", e
	}
	return verified, relay, nil
}

func (p *SAMLServiceProvider) VerifyXML(raw []byte, requestID string) (result *SAMLVerifiedAssertion, err error) {
	// Last-resort fail closed against dependency parser panics. Structural checks
	// below prevent known nil dereferences; this does not replace those checks.
	defer func() {
		if recover() != nil {
			result = nil
			err = ErrInvalidSAMLProof
		}
	}()
	if p == nil || p.digest == "" || !samlIDPattern.MatchString(requestID) {
		return nil, ErrInvalidSAMLProof
	}
	now := time.Now().UTC()
	if now.Before(p.certificate.NotBefore) || !now.Before(p.certificate.NotAfter) {
		return nil, ErrInvalidSAMLProof
	}
	responseID, e := p.preflight(raw, requestID, now)
	if e != nil {
		return nil, ErrInvalidSAMLProof
	}
	a, e := p.sp.ParseXMLResponse(raw, []string{requestID}, p.sp.AcsURL)
	if e != nil || a == nil {
		return nil, ErrInvalidSAMLProof
	}
	// Values below come only from the library's signature-verified assertion.
	if a.Subject == nil || a.Subject.NameID == nil || a.Conditions == nil || len(a.Subject.SubjectConfirmations) != 1 || len(a.Conditions.AudienceRestrictions) != 1 || len(a.AuthnStatements) != 1 || a.Issuer.Value != p.config.IDPEntityID || a.Version != "2.0" || !samlIDPattern.MatchString(a.ID) || !samlIssueTime(a.IssueInstant, now) {
		return nil, ErrInvalidSAMLProof
	}
	n := a.Subject.NameID
	c := a.Conditions
	sc := a.Subject.SubjectConfirmations[0]
	d := sc.SubjectConfirmationData
	if n.Format != samlPersistentNameID || !boundedSAMLString(n.Value, 1024) || (n.NameQualifier != "" && n.NameQualifier != p.config.IDPEntityID) || (n.SPNameQualifier != "" && n.SPNameQualifier != p.config.SPEntityID) || n.SPProvidedID != "" || c.AudienceRestrictions[0].Audience.Value != p.config.SPEntityID || d == nil || sc.Method != samlBearerMethod || sc.NameID != nil || d.Recipient != p.config.ACSURL || d.InResponseTo != requestID || !d.NotBefore.IsZero() || d.Address != "" {
		return nil, ErrInvalidSAMLProof
	}
	if c.NotBefore.IsZero() || c.NotOnOrAfter.IsZero() || !c.NotOnOrAfter.After(c.NotBefore) || c.NotOnOrAfter.Sub(c.NotBefore) > SAMLRequestTTL || c.NotBefore.After(now.Add(samlClockSkew)) || !now.Before(c.NotOnOrAfter) || c.NotOnOrAfter.After(now.Add(SAMLRequestTTL+samlClockSkew)) || d.NotOnOrAfter.IsZero() || !now.Before(d.NotOnOrAfter) || d.NotOnOrAfter.After(c.NotOnOrAfter) {
		return nil, ErrInvalidSAMLProof
	}
	statement := a.AuthnStatements[0]
	if statement.SessionNotOnOrAfter != nil && !now.Before(*statement.SessionNotOnOrAfter) {
		return nil, ErrInvalidSAMLProof
	}
	aTime := statement.AuthnInstant
	if aTime.IsZero() || aTime.After(now.Add(samlClockSkew)) || aTime.Before(now.Add(-SAMLRequestTTL)) {
		return nil, ErrInvalidSAMLProof
	}
	expiry := c.NotOnOrAfter
	if statement.SessionNotOnOrAfter != nil && statement.SessionNotOnOrAfter.Before(expiry) {
		expiry = *statement.SessionNotOnOrAfter
	}
	if d.NotOnOrAfter.Before(expiry) {
		expiry = d.NotOnOrAfter
	}
	return &SAMLVerifiedAssertion{assertionID: a.ID, responseID: responseID, requestID: requestID, issuer: a.Issuer.Value, subject: n.Value, nameIDFormat: n.Format, nameQualifier: n.NameQualifier, spNameQualifier: n.SPNameQualifier, configDigest: p.digest, accountID: p.config.AccountID, configVersion: p.config.ConfigVersion, expiresAt: expiry}, nil
}
func samlIssueTime(t, now time.Time) bool {
	return !t.IsZero() && !t.After(now.Add(samlClockSkew)) && !t.Before(now.Add(-samlMaxIssueAge))
}

// boundedXML is an allocation-bounded rejection-only pass before etree and the
// round-trip validator. Never use this parse to authenticate identity claims.
func boundedSAMLXML(raw []byte) error {
	if len(raw) == 0 || len(raw) > SAMLMaxXMLBytes {
		return ErrInvalidSAMLProof
	}
	d := xml.NewDecoder(bytes.NewReader(raw))
	depth, tokens, attrs, roots := 0, 0, 0, 0
	seenRoot, seenDeclaration := false, false
	for {
		tok, e := d.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return ErrInvalidSAMLProof
		}
		tokens++
		if tokens > SAMLMaxXMLTokens {
			return ErrInvalidSAMLProof
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
				seenRoot = true
			}
			depth++
			attrs += len(t.Attr)
			if depth > SAMLMaxXMLDepth || len(t.Attr) > 32 || attrs > 10000 {
				return ErrInvalidSAMLProof
			}
			names := map[xml.Name]bool{}
			for _, a := range t.Attr {
				if names[a.Name] {
					return ErrInvalidSAMLProof
				}
				names[a.Name] = true
				if a.Name.Space != "" && a.Name.Space != "xmlns" {
					return ErrInvalidSAMLProof
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive, xml.Comment:
			return ErrInvalidSAMLProof
		case xml.ProcInst:
			if seenRoot || seenDeclaration || t.Target != "xml" {
				return ErrInvalidSAMLProof
			}
			seenDeclaration = true
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) != 0 {
				return ErrInvalidSAMLProof
			}
		}
	}
	if depth != 0 || roots != 1 {
		return ErrInvalidSAMLProof
	}
	return nil
}

func samlChildren(el *etree.Element, ns, tag string) []*etree.Element {
	var out []*etree.Element
	for _, c := range el.ChildElements() {
		if c.NamespaceURI() == ns && c.Tag == tag {
			out = append(out, c)
		}
	}
	return out
}
func samlOne(el *etree.Element, ns, tag string) (*etree.Element, error) {
	a := samlChildren(el, ns, tag)
	if len(a) != 1 {
		return nil, ErrInvalidSAMLProof
	}
	return a[0], nil
}
func samlShape(el *etree.Element, ns string, allowed ...string) bool {
	for _, c := range el.ChildElements() {
		valid := false
		for _, name := range allowed {
			if c.NamespaceURI() == ns && c.Tag == name {
				valid = true
			}
		}
		if !valid {
			return false
		}
	}
	return true
}
func samlText(el *etree.Element) bool {
	return len(el.ChildElements()) == 0 && boundedSAMLString(el.Text(), 4096)
}
func samlAttrs(el *etree.Element, allowed ...string) bool {
	for _, a := range el.Attr {
		if a.Space == "xmlns" || (a.Space == "" && a.Key == "xmlns") {
			continue
		}
		ok := false
		for _, k := range allowed {
			if a.Space == "" && a.Key == k {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
func samlTimeAttr(el *etree.Element, name string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, el.SelectAttrValue(name, ""))
}

func (p *SAMLServiceProvider) preflight(raw []byte, requestID string, now time.Time) (string, error) {
	if e := boundedSAMLXML(raw); e != nil {
		return "", e
	}
	doc := etree.NewDocument()
	if e := doc.ReadFromBytes(raw); e != nil {
		return "", ErrInvalidSAMLProof
	}
	r := doc.Root()
	if r == nil || r.NamespaceURI() != samlProtocolNS || r.Tag != "Response" || !samlAttrs(r, "ID", "Version", "IssueInstant", "Destination", "InResponseTo") || r.SelectAttrValue("Version", "") != "2.0" || r.SelectAttrValue("Destination", "") != p.config.ACSURL || r.SelectAttrValue("InResponseTo", "") != requestID {
		return "", ErrInvalidSAMLProof
	}
	responseID := r.SelectAttrValue("ID", "")
	if !samlIDPattern.MatchString(responseID) {
		return "", ErrInvalidSAMLProof
	}
	issued, e := samlTimeAttr(r, "IssueInstant")
	if e != nil || !samlIssueTime(issued, now) {
		return "", ErrInvalidSAMLProof
	}
	// Response signatures are deliberately unsupported: crewjam otherwise skips
	// assertion-signature verification. Every accepted proof is assertion-signed.
	for _, c := range r.ChildElements() {
		if !((c.NamespaceURI() == samlAssertionNS && (c.Tag == "Issuer" || c.Tag == "Assertion")) || (c.NamespaceURI() == samlProtocolNS && c.Tag == "Status")) {
			return "", ErrInvalidSAMLProof
		}
	}
	issuer, e := samlOne(r, samlAssertionNS, "Issuer")
	if e != nil || !samlText(issuer) || issuer.Text() != p.config.IDPEntityID {
		return "", ErrInvalidSAMLProof
	}
	status, e := samlOne(r, samlProtocolNS, "Status")
	if e != nil || !samlShape(status, samlProtocolNS, "StatusCode") {
		return "", ErrInvalidSAMLProof
	}
	code, e := samlOne(status, samlProtocolNS, "StatusCode")
	if e != nil || len(code.ChildElements()) != 0 || code.SelectAttrValue("Value", "") != saml.StatusSuccess {
		return "", ErrInvalidSAMLProof
	}
	a, e := samlOne(r, samlAssertionNS, "Assertion")
	if e != nil || !samlAttrs(a, "ID", "Version", "IssueInstant") || a.SelectAttrValue("Version", "") != "2.0" {
		return "", ErrInvalidSAMLProof
	}
	assertionID := a.SelectAttrValue("ID", "")
	if !samlIDPattern.MatchString(assertionID) || assertionID == responseID {
		return "", ErrInvalidSAMLProof
	}
	ids := map[string]bool{}
	var walk func(*etree.Element) bool
	walk = func(el *etree.Element) bool {
		for _, attr := range el.Attr {
			if strings.EqualFold(attr.Key, "id") && attr.Space != "xmlns" {
				if attr.Key != "ID" || attr.Space != "" || !samlIDPattern.MatchString(attr.Value) || ids[attr.Value] {
					return false
				}
				ids[attr.Value] = true
			}
		}
		for _, c := range el.ChildElements() {
			if !walk(c) {
				return false
			}
		}
		return true
	}
	if !walk(r) {
		return "", ErrInvalidSAMLProof
	}
	for _, c := range a.ChildElements() {
		if c.NamespaceURI() == samlDSigNS && c.Tag == "Signature" {
			continue
		}
		if c.NamespaceURI() != samlAssertionNS {
			return "", ErrInvalidSAMLProof
		}
		switch c.Tag {
		case "Issuer", "Subject", "Conditions", "AuthnStatement", "AttributeStatement":
		default:
			return "", ErrInvalidSAMLProof
		}
	}
	ai, e := samlOne(a, samlAssertionNS, "Issuer")
	if e != nil || !samlText(ai) {
		return "", ErrInvalidSAMLProof
	}
	sub, e := samlOne(a, samlAssertionNS, "Subject")
	if e != nil || !samlShape(sub, samlAssertionNS, "NameID", "SubjectConfirmation") {
		return "", ErrInvalidSAMLProof
	}
	name, e := samlOne(sub, samlAssertionNS, "NameID")
	if e != nil || !samlText(name) || !samlAttrs(name, "Format", "NameQualifier", "SPNameQualifier") {
		return "", ErrInvalidSAMLProof
	}
	conf, e := samlOne(sub, samlAssertionNS, "SubjectConfirmation")
	if e != nil || !samlAttrs(conf, "Method") || !samlShape(conf, samlAssertionNS, "SubjectConfirmationData") {
		return "", ErrInvalidSAMLProof
	}
	data, e := samlOne(conf, samlAssertionNS, "SubjectConfirmationData")
	if e != nil || len(data.ChildElements()) != 0 || !samlAttrs(data, "InResponseTo", "Recipient", "NotOnOrAfter") {
		return "", ErrInvalidSAMLProof
	}
	conditions, e := samlOne(a, samlAssertionNS, "Conditions")
	if e != nil || !samlAttrs(conditions, "NotBefore", "NotOnOrAfter") || !samlShape(conditions, samlAssertionNS, "AudienceRestriction") {
		return "", ErrInvalidSAMLProof
	}
	restriction, e := samlOne(conditions, samlAssertionNS, "AudienceRestriction")
	if e != nil || !samlShape(restriction, samlAssertionNS, "Audience") {
		return "", ErrInvalidSAMLProof
	}
	audience, e := samlOne(restriction, samlAssertionNS, "Audience")
	if e != nil || !samlText(audience) {
		return "", ErrInvalidSAMLProof
	}
	statement, e := samlOne(a, samlAssertionNS, "AuthnStatement")
	if e != nil || !samlAttrs(statement, "AuthnInstant", "SessionIndex", "SessionNotOnOrAfter") || !samlShape(statement, samlAssertionNS, "AuthnContext") {
		return "", ErrInvalidSAMLProof
	}
	context, e := samlOne(statement, samlAssertionNS, "AuthnContext")
	if e != nil || !samlShape(context, samlAssertionNS, "AuthnContextClassRef") {
		return "", ErrInvalidSAMLProof
	}
	class, e := samlOne(context, samlAssertionNS, "AuthnContextClassRef")
	if e != nil || !samlText(class) {
		return "", ErrInvalidSAMLProof
	}
	if len(samlChildren(a, samlAssertionNS, "AttributeStatement")) > 1 {
		return "", ErrInvalidSAMLProof
	}
	for _, attrs := range samlChildren(a, samlAssertionNS, "AttributeStatement") {
		if !samlShape(attrs, samlAssertionNS, "Attribute") {
			return "", ErrInvalidSAMLProof
		}
		for _, attr := range attrs.ChildElements() {
			if !samlAttrs(attr, "Name", "NameFormat", "FriendlyName") || !samlShape(attr, samlAssertionNS, "AttributeValue") {
				return "", ErrInvalidSAMLProof
			}
			for _, v := range attr.ChildElements() {
				if len(v.ChildElements()) != 0 {
					return "", ErrInvalidSAMLProof
				}
			}
		}
	}
	sig, e := samlOne(a, samlDSigNS, "Signature")
	if e != nil || !p.signatureShape(sig, assertionID) {
		return "", ErrInvalidSAMLProof
	}
	return responseID, nil
}

func (p *SAMLServiceProvider) signatureShape(sig *etree.Element, id string) bool {
	if !samlShape(sig, samlDSigNS, "SignedInfo", "SignatureValue", "KeyInfo") {
		return false
	}
	info, e := samlOne(sig, samlDSigNS, "SignedInfo")
	if e != nil || !samlShape(info, samlDSigNS, "CanonicalizationMethod", "SignatureMethod", "Reference") {
		return false
	}
	c, e := samlOne(info, samlDSigNS, "CanonicalizationMethod")
	if e != nil || c.SelectAttrValue("Algorithm", "") != samlExclusiveC14N || len(c.ChildElements()) != 0 {
		return false
	}
	method, e := samlOne(info, samlDSigNS, "SignatureMethod")
	if e != nil || len(method.ChildElements()) != 0 {
		return false
	}
	switch method.SelectAttrValue("Algorithm", "") {
	case "http://www.w3.org/2001/04/xmldsig-more#rsa-sha256", "http://www.w3.org/2001/04/xmldsig-more#rsa-sha384", "http://www.w3.org/2001/04/xmldsig-more#rsa-sha512":
	default:
		return false
	}
	ref, e := samlOne(info, samlDSigNS, "Reference")
	if e != nil || ref.SelectAttrValue("URI", "") != "#"+id || !samlShape(ref, samlDSigNS, "Transforms", "DigestMethod", "DigestValue") {
		return false
	}
	transforms, e := samlOne(ref, samlDSigNS, "Transforms")
	if e != nil || !samlShape(transforms, samlDSigNS, "Transform") {
		return false
	}
	t := transforms.ChildElements()
	if len(t) != 2 || t[0].SelectAttrValue("Algorithm", "") != samlDSigNS+"enveloped-signature" || t[1].SelectAttrValue("Algorithm", "") != samlExclusiveC14N || len(t[0].ChildElements()) != 0 || len(t[1].ChildElements()) != 0 {
		return false
	}
	digest, e := samlOne(ref, samlDSigNS, "DigestMethod")
	if e != nil || len(digest.ChildElements()) != 0 {
		return false
	}
	expectedDigest := map[string]string{
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha256": "http://www.w3.org/2001/04/xmlenc#sha256",
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha384": "http://www.w3.org/2001/04/xmldsig-more#sha384",
		"http://www.w3.org/2001/04/xmldsig-more#rsa-sha512": "http://www.w3.org/2001/04/xmlenc#sha512",
	}[method.SelectAttrValue("Algorithm", "")]
	if expectedDigest == "" || digest.SelectAttrValue("Algorithm", "") != expectedDigest {
		return false
	}
	value, e := samlOne(ref, samlDSigNS, "DigestValue")
	if e != nil || !samlText(value) {
		return false
	}
	value, e = samlOne(sig, samlDSigNS, "SignatureValue")
	if e != nil || !samlText(value) {
		return false
	}
	keys := samlChildren(sig, samlDSigNS, "KeyInfo")
	if len(keys) > 1 {
		return false
	}
	if len(keys) == 1 {
		if !samlShape(keys[0], samlDSigNS, "X509Data") {
			return false
		}
		data, e := samlOne(keys[0], samlDSigNS, "X509Data")
		if e != nil || !samlShape(data, samlDSigNS, "X509Certificate") {
			return false
		}
		cert, e := samlOne(data, samlDSigNS, "X509Certificate")
		if e != nil || !samlText(cert) {
			return false
		}
		der, e := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(cert.Text()), ""))
		if e != nil || !bytes.Equal(der, p.certificate.Raw) {
			return false
		}
	}
	return true
}
