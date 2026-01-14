package identify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// FacetToFactMapping maps facet types from PII extraction to person_facts fields
var FacetToFactMapping = map[string]struct {
	Category string
	FactType string
}{
	"pii_email_personal":   {CategoryContactInfo, FactTypeEmailPersonal},
	"pii_email_work":       {CategoryContactInfo, FactTypeEmailWork},
	"pii_email_school":     {CategoryContactInfo, FactTypeEmailSchool},
	"pii_phone_mobile":     {CategoryContactInfo, FactTypePhoneMobile},
	"pii_phone_home":       {CategoryContactInfo, FactTypePhoneHome},
	"pii_phone_work":       {CategoryContactInfo, FactTypePhoneWork},
	"pii_full_legal_name":  {CategoryCoreIdentity, FactTypeFullLegalName},
	"pii_given_name":       {CategoryCoreIdentity, FactTypeGivenName},
	"pii_family_name":      {CategoryCoreIdentity, FactTypeFamilyName},
	"pii_birthdate":        {CategoryCoreIdentity, FactTypeBirthdate},
	"pii_employer_current": {CategoryProfessional, FactTypeEmployerCurrent},
	"pii_business_owned":   {CategoryProfessional, FactTypeBusinessOwned},
	"pii_business_role":    {CategoryProfessional, FactTypeBusinessRole},
	"pii_profession":       {CategoryProfessional, FactTypeProfession},
	"pii_location_current": {CategoryLocation, FactTypeLocationCurrent},
	"pii_spouse_first_name": {CategoryRelationships, FactTypeSpouseFirstName},
	"pii_school_attended":  {CategoryEducation, FactTypeSchoolAttended},
	"pii_social_twitter":   {CategoryDigitalIdentity, FactTypeSocialTwitter},
	"pii_social_instagram": {CategoryDigitalIdentity, FactTypeSocialInstagram},
	"pii_social_linkedin":  {CategoryDigitalIdentity, FactTypeSocialLinkedIn},
	"pii_social_facebook":  {CategoryDigitalIdentity, FactTypeSocialFacebook},
	"pii_username_generic": {CategoryDigitalIdentity, FactTypeUsernameGeneric},
	"pii_ssn":              {CategoryGovernmentID, FactTypeSSN},
	"pii_passport_number":  {CategoryGovernmentID, FactTypePassportNumber},
	"pii_drivers_license":  {CategoryGovernmentID, FactTypeDriversLicense},
}

// SyncStats holds statistics about a sync operation
type SyncStats struct {
	AnalysisRunsProcessed int
	FacetsProcessed       int
	FactsCreated          int
	FactsUpdated          int
	UnattributedCreated   int
	ThirdPartiesCreated   int
	Errors                int
}

// SyncFacetsToPersonFacts processes facets from pii_extraction analysis runs
// and creates/updates person_facts entries
func SyncFacetsToPersonFacts(db *sql.DB) (*SyncStats, error) {
	stats := &SyncStats{}

	// Get completed pii_extraction analysis runs that haven't been synced
	// We track sync status by checking if facets have corresponding person_facts
	rows, err := db.Query(`
		SELECT DISTINCT ar.id, ar.conversation_id
		FROM analysis_runs ar
		JOIN analysis_types at ON ar.analysis_type_id = at.id
		WHERE at.name = 'pii_extraction'
		AND ar.status = 'completed'
		AND EXISTS (
			SELECT 1 FROM facets f 
			WHERE f.analysis_run_id = ar.id
		)
	`)
	if err != nil {
		return nil, fmt.Errorf("query analysis runs: %w", err)
	}

	var runs []struct {
		ID             string
		ConversationID string
	}
	for rows.Next() {
		var run struct {
			ID             string
			ConversationID string
		}
		if err := rows.Scan(&run.ID, &run.ConversationID); err != nil {
			rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, run := range runs {
		runStats, err := syncAnalysisRun(db, run.ID, run.ConversationID)
		if err != nil {
			stats.Errors++
			continue
		}
		stats.AnalysisRunsProcessed++
		stats.FacetsProcessed += runStats.FacetsProcessed
		stats.FactsCreated += runStats.FactsCreated
		stats.FactsUpdated += runStats.FactsUpdated
		stats.UnattributedCreated += runStats.UnattributedCreated
		stats.ThirdPartiesCreated += runStats.ThirdPartiesCreated
	}

	return stats, nil
}

// syncAnalysisRun processes a single analysis run's facets
func syncAnalysisRun(db *sql.DB, runID, conversationID string) (*SyncStats, error) {
	stats := &SyncStats{}

	// Get the channel for this conversation (for source_channel)
	var channel sql.NullString
	err := db.QueryRow(`
		SELECT channel FROM conversations WHERE id = ?
	`, conversationID).Scan(&channel)
	if err != nil {
		return nil, fmt.Errorf("get conversation channel: %w", err)
	}

	// Get all facets for this analysis run
	rows, err := db.Query(`
		SELECT id, facet_type, value, person_id, confidence, metadata_json
		FROM facets
		WHERE analysis_run_id = ?
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query facets: %w", err)
	}
	defer rows.Close()

	now := time.Now().Unix()

	for rows.Next() {
		var facetID, facetType, value string
		var personID sql.NullString
		var confidence sql.NullFloat64
		var metadataJSON sql.NullString

		if err := rows.Scan(&facetID, &facetType, &value, &personID, &confidence, &metadataJSON); err != nil {
			stats.Errors++
			continue
		}
		stats.FacetsProcessed++

		// Skip empty values
		if value == "" {
			continue
		}

		// Map facet type to fact type
		mapping, ok := FacetToFactMapping[facetType]
		if !ok {
			continue // Unknown facet type, skip
		}

		// Extract source type and evidence from metadata
		sourceType := "extracted"
		var evidence *string
		if metadataJSON.Valid {
			var meta map[string]interface{}
			if json.Unmarshal([]byte(metadataJSON.String), &meta) == nil {
				if st, ok := meta["source_type"].(string); ok {
					sourceType = st
				}
				if ev, ok := meta["evidence"].(string); ok {
					evidence = &ev
				}
			}
		}

		// If no person_id, this is an unattributed fact
		if !personID.Valid || personID.String == "" {
			// Insert into unattributed_facts
			_, err := db.Exec(`
				INSERT INTO unattributed_facts (
					id, fact_type, fact_value, source_conversation_id, context, created_at
				) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING
			`, uuid.New().String(), mapping.FactType, value, conversationID, metadataJSON.String, now)
			if err == nil {
				stats.UnattributedCreated++
			}
			continue
		}

		// Create person fact
		fact := PersonFact{
			PersonID:           personID.String,
			Category:           mapping.Category,
			FactType:           mapping.FactType,
			FactValue:          value,
			Confidence:         0.5,
			SourceType:         sourceType,
			SourceConversation: &conversationID,
			SourceFacetID:      &facetID,
			Evidence:           evidence,
		}

		if confidence.Valid {
			fact.Confidence = confidence.Float64
		}
		if channel.Valid {
			fact.SourceChannel = &channel.String
		}

		// Determine if sensitive
		fact.IsSensitive = isSensitiveFactType(mapping.FactType)

		err := InsertFact(db, fact)
		if err != nil {
			stats.Errors++
		} else {
			stats.FactsCreated++
		}
	}

	return stats, rows.Err()
}

// SyncSingleRun processes a single analysis run by ID
func SyncSingleRun(db *sql.DB, runID string) (*SyncStats, error) {
	var conversationID string
	err := db.QueryRow(`SELECT conversation_id FROM analysis_runs WHERE id = ?`, runID).Scan(&conversationID)
	if err != nil {
		return nil, fmt.Errorf("get analysis run: %w", err)
	}
	return syncAnalysisRun(db, runID, conversationID)
}

// isSensitiveFactType determines if a fact type should be marked as sensitive
func isSensitiveFactType(factType string) bool {
	sensitiveTypes := map[string]bool{
		FactTypeSSN:            true,
		FactTypePassportNumber: true,
		FactTypeDriversLicense: true,
	}
	return sensitiveTypes[factType]
}

// ProcessPIIExtractionOutput processes the full JSON output from PII extraction
// This is called after the LLM returns structured output to sync ALL extracted data
func ProcessPIIExtractionOutput(db *sql.DB, runID, conversationID, outputJSON string) (*SyncStats, error) {
	stats := &SyncStats{}

	var output struct {
		ExtractionMetadata struct {
			Channel                  string `json:"channel"`
			PrimaryContactName       string `json:"primary_contact_name"`
			PrimaryContactIdentifier string `json:"primary_contact_identifier"`
		} `json:"extraction_metadata"`
		Persons []struct {
			Reference        string                            `json:"reference"`
			IsPrimaryContact bool                              `json:"is_primary_contact"`
			PII              map[string]map[string]interface{} `json:"pii"`
		} `json:"persons"`
		NewIdentityCandidates []struct {
			Reference  string                 `json:"reference"`
			KnownFacts map[string]interface{} `json:"known_facts"`
			Note       string                 `json:"note"`
		} `json:"new_identity_candidates"`
		UnattributedFacts []struct {
			FactType             string   `json:"fact_type"`
			FactValue            string   `json:"fact_value"`
			SharedBy             string   `json:"shared_by"`
			Context              string   `json:"context"`
			PossibleAttributions []string `json:"possible_attributions"`
		} `json:"unattributed_facts"`
	}

	if err := json.Unmarshal([]byte(outputJSON), &output); err != nil {
		return nil, fmt.Errorf("parse output JSON: %w", err)
	}

	now := time.Now().Unix()
	channel := output.ExtractionMetadata.Channel

	// Process each person's PII
	for _, person := range output.Persons {
		// Find the person_id for this person
		// For primary contact, look up by the identifier
		// For others, we may need to match by reference/name
		var personID string
		if person.IsPrimaryContact {
			// Look up primary contact by identifier
			err := db.QueryRow(`
				SELECT p.id FROM persons p
				JOIN identities i ON p.id = i.person_id
				WHERE i.identifier = ?
				LIMIT 1
			`, output.ExtractionMetadata.PrimaryContactIdentifier).Scan(&personID)
			if err != nil {
				continue // Can't find person
			}
		} else {
			// For non-primary contacts, try to find by canonical_name
			err := db.QueryRow(`
				SELECT id FROM persons 
				WHERE canonical_name LIKE ? OR display_name LIKE ?
				LIMIT 1
			`, "%"+person.Reference+"%", "%"+person.Reference+"%").Scan(&personID)
			if err != nil {
				// This might be a new third party - skip for now
				continue
			}
		}

		// Process all PII categories
		for category, facts := range person.PII {
			for factKey, factData := range facts {
				factMap, ok := factData.(map[string]interface{})
				if !ok {
					continue
				}

				value, _ := factMap["value"].(string)
				if value == "" {
					// Handle array values
					if arr, ok := factMap["value"].([]interface{}); ok && len(arr) > 0 {
						for _, v := range arr {
							if s, ok := v.(string); ok {
								processExtractedFact(db, stats, personID, category, factKey, s, factMap, conversationID, channel, runID, now)
							}
						}
						continue
					}
					continue
				}

				processExtractedFact(db, stats, personID, category, factKey, value, factMap, conversationID, channel, runID, now)
			}
		}
	}

	// Process new identity candidates (third parties)
	for _, candidate := range output.NewIdentityCandidates {
		// Create a new person for this third party
		personID := uuid.New().String()
		name := candidate.Reference
		if givenName, ok := candidate.KnownFacts["given_name"].(string); ok && givenName != "" {
			name = givenName
		}

		_, err := db.Exec(`
			INSERT INTO persons (id, canonical_name, relationship_type, created_at, updated_at)
			VALUES (?, ?, 'third_party', ?, ?)
		`, personID, name, now, now)
		if err == nil {
			stats.ThirdPartiesCreated++

			// Add known facts for this new person
			for factKey, factValue := range candidate.KnownFacts {
				if strVal, ok := factValue.(string); ok && strVal != "" {
					fact := PersonFact{
						PersonID:           personID,
						Category:           CategoryCoreIdentity,
						FactType:           factKey,
						FactValue:          strVal,
						Confidence:         0.5,
						SourceType:         "mentioned",
						SourceConversation: &conversationID,
					}
					if channel != "" {
						fact.SourceChannel = &channel
					}
					InsertFact(db, fact)
				}
			}
		}
	}

	// Process unattributed facts
	for _, uf := range output.UnattributedFacts {
		if uf.FactValue == "" {
			continue
		}

		// Find the person_id of who shared this
		var sharedByPersonID sql.NullString
		if uf.SharedBy != "" {
			db.QueryRow(`
				SELECT id FROM persons 
				WHERE canonical_name LIKE ? OR display_name LIKE ?
				LIMIT 1
			`, "%"+uf.SharedBy+"%", "%"+uf.SharedBy+"%").Scan(&sharedByPersonID)
		}

		attributionsJSON, _ := json.Marshal(uf.PossibleAttributions)

		_, err := db.Exec(`
			INSERT INTO unattributed_facts (
				id, fact_type, fact_value, shared_by_person_id,
				source_conversation_id, context, possible_attributions, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT DO NOTHING
		`, uuid.New().String(), uf.FactType, uf.FactValue, sharedByPersonID,
			conversationID, uf.Context, string(attributionsJSON), now)
		if err == nil {
			stats.UnattributedCreated++
		}
	}

	return stats, nil
}

// processExtractedFact handles inserting a single extracted fact
func processExtractedFact(db *sql.DB, stats *SyncStats, personID, category, factKey, value string, factMap map[string]interface{}, conversationID, channel, runID string, now int64) {
	// Map the fact key to our standard fact types
	factType := mapFactKey(factKey)
	mappedCategory := mapCategory(category)

	confidence := 0.5
	if confStr, ok := factMap["confidence"].(string); ok {
		switch confStr {
		case "high":
			confidence = 0.9
		case "medium":
			confidence = 0.7
		case "low":
			confidence = 0.4
		}
	}

	sourceType := "mentioned"
	if selfDisclosed, ok := factMap["self_disclosed"].(bool); ok && selfDisclosed {
		sourceType = "self_disclosed"
	}
	if source, ok := factMap["source"].(string); ok {
		sourceType = source
	}

	var evidence *string
	if evidenceArr, ok := factMap["evidence"].([]interface{}); ok && len(evidenceArr) > 0 {
		combined := ""
		for i, e := range evidenceArr {
			if s, ok := e.(string); ok {
				if i > 0 {
					combined += "; "
				}
				combined += s
			}
		}
		if combined != "" {
			evidence = &combined
		}
	}

	fact := PersonFact{
		PersonID:           personID,
		Category:           mappedCategory,
		FactType:           factType,
		FactValue:          value,
		Confidence:         confidence,
		SourceType:         sourceType,
		SourceConversation: &conversationID,
		Evidence:           evidence,
		IsSensitive:        isSensitiveFactType(factType),
	}
	if channel != "" {
		fact.SourceChannel = &channel
	}

	err := InsertFact(db, fact)
	if err == nil {
		stats.FactsCreated++
	}
}

// mapFactKey maps extraction output keys to our standard fact type constants
func mapFactKey(key string) string {
	keyMap := map[string]string{
		"full_legal_name":   FactTypeFullLegalName,
		"given_name":        FactTypeGivenName,
		"family_name":       FactTypeFamilyName,
		"date_of_birth":     FactTypeBirthdate,
		"nicknames":         "nickname",
		"email_personal":    FactTypeEmailPersonal,
		"email_work":        FactTypeEmailWork,
		"email_school":      FactTypeEmailSchool,
		"phone_mobile":      FactTypePhoneMobile,
		"phone_home":        FactTypePhoneHome,
		"phone_work":        FactTypePhoneWork,
		"employer_current":  FactTypeEmployerCurrent,
		"business_owned":    FactTypeBusinessOwned,
		"business_role":     FactTypeBusinessRole,
		"profession":        FactTypeProfession,
		"location_current":  FactTypeLocationCurrent,
		"spouse":            FactTypeSpouseFirstName,
		"school_previous":   FactTypeSchoolAttended,
		"social_twitter":    FactTypeSocialTwitter,
		"social_instagram":  FactTypeSocialInstagram,
		"social_linkedin":   FactTypeSocialLinkedIn,
		"social_facebook":   FactTypeSocialFacebook,
		"username_unknown":  FactTypeUsernameGeneric,
		"ssn":               FactTypeSSN,
		"passport_number":   FactTypePassportNumber,
		"drivers_license":   FactTypeDriversLicense,
	}
	if mapped, ok := keyMap[key]; ok {
		return mapped
	}
	return key
}

// mapCategory maps extraction output categories to our standard category constants
func mapCategory(category string) string {
	catMap := map[string]string{
		"core_identity":        CategoryCoreIdentity,
		"contact_information":  CategoryContactInfo,
		"professional":         CategoryProfessional,
		"relationships":        CategoryRelationships,
		"location_presence":    CategoryLocation,
		"education":            CategoryEducation,
		"government_legal_ids": CategoryGovernmentID,
		"financial":            CategoryFinancial,
		"medical_health":       CategoryMedical,
		"digital_identity":     CategoryDigitalIdentity,
	}
	if mapped, ok := catMap[category]; ok {
		return mapped
	}
	return category
}
