package modruntime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// These codecs are only for private mode-0600 IAM owner backups. Every storage
// field is explicitly represented without relaxing the public model's JSON tags.
const (
	samlRequestSnapshotCodec = "iam.saml_request.v1"
	samlReplaySnapshotCodec  = "iam.saml_replay.v1"
)

type samlRequestSnapshot struct {
	ID                  uint       `json:"id"`
	RequestID           string     `json:"request_id"`
	AccountID           uint       `json:"account_id"`
	ConfigVersion       uint64     `json:"config_version"`
	ConfigDigest        string     `json:"config_digest"`
	RelayDigest         string     `json:"relay_digest"`
	BrowserSecretDigest string     `json:"browser_secret_digest"`
	CreatedAt           time.Time  `json:"created_at"`
	ExpiresAt           time.Time  `json:"expires_at"`
	ConsumedAt          *time.Time `json:"consumed_at"`
}

type samlReplaySnapshot struct {
	ID              uint      `json:"id"`
	AssertionDigest string    `json:"assertion_digest"`
	ResponseDigest  string    `json:"response_digest"`
	CreatedAt       time.Time `json:"created_at"`
	RetainUntil     time.Time `json:"retain_until"`
}

type samlSnapshotTable[T any] struct {
	Codec string `json:"codec"`
	Rows  []T    `json:"rows"`
}

func marshalSAMLRequestSnapshot(rows []domain.SAMLAuthnRequest) ([]byte, error) {
	table := samlSnapshotTable[samlRequestSnapshot]{Codec: samlRequestSnapshotCodec, Rows: make([]samlRequestSnapshot, len(rows))}
	for i, row := range rows {
		table.Rows[i] = samlRequestSnapshot(row)
	}
	return json.Marshal(table)
}

func marshalSAMLReplaySnapshot(rows []domain.SAMLReplay) ([]byte, error) {
	table := samlSnapshotTable[samlReplaySnapshot]{Codec: samlReplaySnapshotCodec, Rows: make([]samlReplaySnapshot, len(rows))}
	for i, row := range rows {
		table.Rows[i] = samlReplaySnapshot(row)
	}
	return json.Marshal(table)
}

// In addition to rejecting unknown fields and trailing data, reject duplicate
// keys and case aliases. encoding/json otherwise accepts both with last-value
// wins semantics, which cannot establish an unambiguous security checkpoint.
func validateSAMLJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("ambiguous SAML snapshot key")
				}
				seen[name] = true
				if err := scan(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := scan(); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid SAML snapshot structure")
		}
		_, err = decoder.Token()
		return err
	}
	if err := scan(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("SAML snapshot contains trailing data")
	}
	return nil
}

func decodeSAMLTable[T any](raw []byte, codec string) ([]T, error) {
	if err := validateSAMLJSONKeys(raw); err != nil {
		return nil, err
	}
	var table samlSnapshotTable[T]
	if err := decodeStrictSnapshotJSON(raw, &table); err != nil {
		return nil, err
	}
	if table.Codec != codec || table.Rows == nil {
		return nil, errors.New("unsupported SAML snapshot codec")
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil || len(shape) != 2 || shape["codec"] == nil || shape["rows"] == nil {
		return nil, errors.New("invalid SAML snapshot table fields")
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(shape["rows"], &rows); err != nil {
		return nil, err
	}
	var item T
	typ := reflect.TypeOf(item)
	for _, row := range rows {
		if len(row) != typ.NumField() {
			return nil, errors.New("incomplete SAML snapshot row")
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			value, present := row[field.Tag.Get("json")]
			if !present || (field.Type.Kind() != reflect.Pointer && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
				return nil, errors.New("missing or null SAML snapshot field")
			}
		}
	}
	return table.Rows, nil
}

func validSAMLSnapshotDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

var samlSnapshotRequestID = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,255}$`)

func validSAMLRequestSnapshot(row domain.SAMLAuthnRequest) bool {
	return row.ID != 0 && samlSnapshotRequestID.MatchString(row.RequestID) && row.AccountID != 0 && row.ConfigVersion != 0 &&
		validSAMLSnapshotDigest(row.ConfigDigest) && validSAMLSnapshotDigest(row.RelayDigest) && validSAMLSnapshotDigest(row.BrowserSecretDigest) &&
		!row.CreatedAt.IsZero() && row.ExpiresAt.After(row.CreatedAt) && row.ExpiresAt.Sub(row.CreatedAt) <= 5*time.Minute &&
		(row.ConsumedAt == nil || !row.ConsumedAt.IsZero())
}

func validSAMLReplaySnapshot(row domain.SAMLReplay) bool {
	return row.ID != 0 && validSAMLSnapshotDigest(row.AssertionDigest) && validSAMLSnapshotDigest(row.ResponseDigest) &&
		!row.CreatedAt.IsZero() && !row.RetainUntil.Before(row.CreatedAt)
}

func unmarshalSAMLRequestSnapshot(raw []byte, target *[]domain.SAMLAuthnRequest) error {
	rows, err := decodeSAMLTable[samlRequestSnapshot](raw, samlRequestSnapshotCodec)
	if err != nil {
		return err
	}
	result := make([]domain.SAMLAuthnRequest, len(rows))
	ids, requests, relays := map[uint]bool{}, map[string]bool{}, map[string]bool{}
	for i, row := range rows {
		value := domain.SAMLAuthnRequest(row)
		if !validSAMLRequestSnapshot(value) || ids[row.ID] || requests[row.RequestID] || relays[row.RelayDigest] {
			return errors.New("invalid SAML request checkpoint")
		}
		ids[row.ID], requests[row.RequestID], relays[row.RelayDigest] = true, true, true
		result[i] = value
	}
	*target = result
	return nil
}

func unmarshalSAMLReplaySnapshot(raw []byte, target *[]domain.SAMLReplay) error {
	rows, err := decodeSAMLTable[samlReplaySnapshot](raw, samlReplaySnapshotCodec)
	if err != nil {
		return err
	}
	result := make([]domain.SAMLReplay, len(rows))
	ids, assertions, responses := map[uint]bool{}, map[string]bool{}, map[string]bool{}
	for i, row := range rows {
		value := domain.SAMLReplay(row)
		if !validSAMLReplaySnapshot(value) || ids[row.ID] || assertions[row.AssertionDigest] || responses[row.ResponseDigest] {
			return errors.New("invalid SAML replay checkpoint")
		}
		ids[row.ID], assertions[row.AssertionDigest], responses[row.ResponseDigest] = true, true, true
		result[i] = value
	}
	*target = result
	return nil
}

// Recovery is a security boundary, not a continuation of a browser login.
// Existing consumed timestamps stay unchanged; every restored pending request
// becomes a deterministic terminal tombstone. A fresh AuthnRequest is required.
func restoreSAMLSnapshotRows(tx *gorm.DB, rows any) (bool, error) {
	if requests, ok := rows.(*[]domain.SAMLAuthnRequest); ok {
		for i := range *requests {
			if (*requests)[i].ConsumedAt == nil {
				expires := (*requests)[i].ExpiresAt
				(*requests)[i].ConsumedAt = &expires
			}
		}
		if len(*requests) == 0 {
			return true, nil
		}
		return true, tx.Create(requests).Error
	}
	replays, ok := rows.(*[]domain.SAMLReplay)
	if !ok {
		return false, nil
	}
	for _, incoming := range *replays {
		var retained []domain.SAMLReplay
		if err := tx.Where("assertion_digest = ? OR response_digest = ?", incoming.AssertionDigest, incoming.ResponseDigest).Find(&retained).Error; err != nil {
			return true, err
		}
		if len(retained) == 0 {
			// Snapshot numeric IDs are local surrogate keys. Preserve both
			// security identities while allocating around retained target rows.
			// Explicit restored IDs may leave PostgreSQL's sequence behind;
			// allocate above the retained maximum and reset sequences afterward.
			var maximum uint
			if err := tx.Model(&domain.SAMLReplay{}).Select("COALESCE(MAX(id), 0)").Scan(&maximum).Error; err != nil {
				return true, err
			}
			if maximum == ^uint(0) || uint64(maximum) >= math.MaxInt64 {
				return true, errors.New("SAML replay checkpoint identity exhausted")
			}
			incoming.ID = maximum + 1
			if err := tx.Create(&incoming).Error; err != nil {
				return true, err
			}
			continue
		}
		if len(retained) != 1 || !validSAMLReplaySnapshot(retained[0]) || retained[0].AssertionDigest != incoming.AssertionDigest || retained[0].ResponseDigest != incoming.ResponseDigest {
			// A partial pair collision cannot represent both facts in this
			// schema. Reject the whole restore rather than drop either guard.
			return true, errors.New("conflicting SAML replay checkpoint")
		}
		current := retained[0]
		if incoming.CreatedAt.Before(current.CreatedAt) {
			current.CreatedAt = incoming.CreatedAt
		}
		if incoming.RetainUntil.After(current.RetainUntil) {
			current.RetainUntil = incoming.RetainUntil
		}
		if err := tx.Model(&domain.SAMLReplay{}).Where("id = ?", current.ID).Updates(map[string]any{"created_at": current.CreatedAt, "retain_until": current.RetainUntil}).Error; err != nil {
			return true, err
		}
	}
	return true, nil
}
