package contacts

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// DBTX abstracts *sql.DB and *sql.Tx for shared helpers.
type DBTX interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// NormalizeIdentifier returns a normalized identifier for dedupe.
func NormalizeIdentifier(value, identifierType string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch identifierType {
	case "email":
		return strings.ToLower(value)
	case "phone":
		return normalizePhone(value)
	case "handle":
		return normalizeHandle(value)
	default:
		return strings.ToLower(value)
	}
}

func normalizeHandle(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "@") {
		return value
	}
	return "@" + value
}

// normalizePhone removes non-digits and drops a leading US 1 when present.
func normalizePhone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	digits := b.String()
	if len(digits) == 11 && strings.HasPrefix(digits, "1") {
		return digits[1:]
	}
	return digits
}

func looksLikeEmail(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, "@")
}

func looksLikePhone(value string) bool {
	normalized := normalizePhone(value)
	if normalized == "" {
		return false
	}
	if len(normalized) < 7 || len(normalized) > 15 {
		return false
	}
	for _, r := range value {
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case ' ', '-', '(', ')', '+', '.':
			continue
		default:
			return false
		}
	}
	return true
}

func isAllDigits(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// IsMeaningfulPersonName returns true if a string looks like a human name.
func IsMeaningfulPersonName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	if lower == "unknown" || lower == "unknown contact" || lower == "me" {
		return false
	}
	if looksLikeEmail(name) || looksLikePhone(name) {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(name), "@") {
		return false
	}
	if isAllDigits(name) {
		return false
	}
	return true
}

// isGenericName is a looser check for contact display names.
func isGenericName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	lower := strings.ToLower(name)
	if lower == "unknown" || lower == "unknown contact" || lower == "me" {
		return true
	}
	if looksLikeEmail(name) || looksLikePhone(name) {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(name), "@") {
		return true
	}
	if isAllDigits(name) {
		return true
	}
	for _, r := range name {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func chooseDisplayName(existing, candidate, fallback string) string {
	if IsMeaningfulPersonName(candidate) && isGenericName(existing) {
		return strings.TrimSpace(candidate)
	}
	if existing == "" {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
		return fallback
	}
	return existing
}

func getContactIDByIdentifier(db DBTX, identifierType, normalized string) (string, error) {
	var contactID string
	err := db.QueryRow(`
		SELECT contact_id FROM contact_identifiers
		WHERE type = ? AND normalized = ?
	`, identifierType, normalized).Scan(&contactID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return contactID, nil
}

// GetOrCreateContact returns the contact ID for an identifier, creating a new contact if needed.
func GetOrCreateContact(db DBTX, identifierType, rawValue, displayName, source string) (string, bool, error) {
	normalized := NormalizeIdentifier(rawValue, identifierType)
	if normalized == "" {
		return "", false, fmt.Errorf("empty %s identifier", identifierType)
	}
	if source == "" {
		source = "unknown"
	}
	now := time.Now().Unix()

	if existingID, err := getContactIDByIdentifier(db, identifierType, normalized); err != nil {
		return "", false, fmt.Errorf("lookup contact: %w", err)
	} else if existingID != "" {
		if err := updateContactName(db, existingID, displayName, normalized, now); err != nil {
			return "", false, err
		}
		_, _ = db.Exec(`
			UPDATE contact_identifiers
			SET value = ?, last_seen_at = ?
			WHERE type = ? AND normalized = ?
		`, strings.TrimSpace(rawValue), now, identifierType, normalized)
		return existingID, false, nil
	}

	contactID := uuid.New().String()
	display := chooseDisplayName("", displayName, normalized)
	if _, err := db.Exec(`
		INSERT INTO contacts (id, display_name, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, contactID, display, source, now, now); err != nil {
		return "", false, fmt.Errorf("insert contact: %w", err)
	}

	identifierID := uuid.New().String()
	if _, err := db.Exec(`
		INSERT INTO contact_identifiers (id, contact_id, type, value, normalized, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, identifierID, contactID, identifierType, strings.TrimSpace(rawValue), normalized, now, now); err != nil {
		if existingID, lookupErr := getContactIDByIdentifier(db, identifierType, normalized); lookupErr == nil && existingID != "" {
			if err := updateContactName(db, existingID, displayName, normalized, now); err != nil {
				return "", false, err
			}
			return existingID, false, nil
		}
		return "", false, fmt.Errorf("insert contact identifier: %w", err)
	}

	return contactID, true, nil
}

func updateContactName(db DBTX, contactID, candidate, normalized string, now int64) error {
	var existing sql.NullString
	if err := db.QueryRow(`SELECT display_name FROM contacts WHERE id = ?`, contactID).Scan(&existing); err != nil {
		return fmt.Errorf("read contact display name: %w", err)
	}
	current := ""
	if existing.Valid {
		current = existing.String
	}
	next := chooseDisplayName(current, candidate, normalized)
	if next == current {
		return nil
	}
	_, err := db.Exec(`
		UPDATE contacts
		SET display_name = ?, updated_at = ?
		WHERE id = ?
	`, next, now, contactID)
	if err != nil {
		return fmt.Errorf("update contact display name: %w", err)
	}
	return nil
}

// EnsurePersonForContact creates or returns a person linked to a contact if the name is meaningful.
func EnsurePersonForContact(db DBTX, contactID, name, sourceType string, confidence float64) (string, bool, error) {
	if !IsMeaningfulPersonName(name) {
		return "", false, nil
	}
	if sourceType == "" {
		sourceType = "deterministic"
	}
	if confidence <= 0 {
		confidence = 1.0
	}
	now := time.Now().Unix()

	var personID string
	if err := db.QueryRow(`
		SELECT person_id FROM person_contact_links
		WHERE contact_id = ?
		ORDER BY confidence DESC, last_seen_at DESC
		LIMIT 1
	`, contactID).Scan(&personID); err == nil && personID != "" {
		if err := updatePersonNameIfGeneric(db, personID, name, now); err != nil {
			return "", false, err
		}
		_ = EnsurePersonContactLink(db, personID, contactID, sourceType, confidence)
		return personID, false, nil
	} else if err != nil && err != sql.ErrNoRows {
		return "", false, fmt.Errorf("lookup person link: %w", err)
	}

	personID = uuid.New().String()
	if _, err := db.Exec(`
		INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?)
	`, personID, strings.TrimSpace(name), now, now); err != nil {
		return "", false, fmt.Errorf("insert person: %w", err)
	}

	if err := insertPersonContactLink(db, personID, contactID, sourceType, confidence, now); err != nil {
		return "", false, err
	}

	return personID, true, nil
}

// EnsurePersonContactLink ensures a link exists and refreshes last_seen_at.
func EnsurePersonContactLink(db DBTX, personID, contactID, sourceType string, confidence float64) error {
	if sourceType == "" {
		sourceType = "deterministic"
	}
	if confidence <= 0 {
		confidence = 1.0
	}
	now := time.Now().Unix()
	return insertPersonContactLink(db, personID, contactID, sourceType, confidence, now)
}

func insertPersonContactLink(db DBTX, personID, contactID, sourceType string, confidence float64, now int64) error {
	_, err := db.Exec(`
		INSERT INTO person_contact_links (
			id, person_id, contact_id, confidence, source_type, first_seen_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(person_id, contact_id) DO UPDATE SET
			last_seen_at = excluded.last_seen_at,
			confidence = CASE
				WHEN excluded.confidence > confidence THEN excluded.confidence
				ELSE confidence
			END
	`, uuid.New().String(), personID, contactID, confidence, sourceType, now, now)
	if err != nil {
		return fmt.Errorf("insert person_contact_link: %w", err)
	}
	return nil
}

func updatePersonNameIfGeneric(db DBTX, personID, name string, now int64) error {
	var canonical, display sql.NullString
	if err := db.QueryRow(`
		SELECT canonical_name, display_name FROM persons WHERE id = ?
	`, personID).Scan(&canonical, &display); err != nil {
		return fmt.Errorf("read person name: %w", err)
	}
	current := ""
	if display.Valid && strings.TrimSpace(display.String) != "" {
		current = display.String
	} else if canonical.Valid {
		current = canonical.String
	}
	if IsMeaningfulPersonName(current) {
		return nil
	}
	if !IsMeaningfulPersonName(name) {
		return nil
	}
	_, err := db.Exec(`
		UPDATE persons
		SET canonical_name = ?, display_name = ?, updated_at = ?
		WHERE id = ?
	`, strings.TrimSpace(name), strings.TrimSpace(name), now, personID)
	if err != nil {
		return fmt.Errorf("update person name: %w", err)
	}
	return nil
}

// GetLinkedPersonID returns a linked person for a contact, if any.
func GetLinkedPersonID(db DBTX, contactID string) (string, error) {
	var personID string
	err := db.QueryRow(`
		SELECT person_id FROM person_contact_links
		WHERE contact_id = ?
		ORDER BY confidence DESC, last_seen_at DESC
		LIMIT 1
	`, contactID).Scan(&personID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup person_contact_links: %w", err)
	}
	return personID, nil
}

// EnsureContactIdentifier attaches an identifier to an existing contact.
func EnsureContactIdentifier(db DBTX, contactID, identifierType, rawValue string) error {
	normalized := NormalizeIdentifier(rawValue, identifierType)
	if normalized == "" {
		return nil
	}
	now := time.Now().Unix()
	_, err := db.Exec(`
		INSERT INTO contact_identifiers (id, contact_id, type, value, normalized, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(type, normalized) DO UPDATE SET
			value = excluded.value,
			last_seen_at = excluded.last_seen_at
	`, uuid.New().String(), contactID, identifierType, strings.TrimSpace(rawValue), normalized, now, now)
	if err != nil {
		return fmt.Errorf("insert contact identifier: %w", err)
	}
	return nil
}

