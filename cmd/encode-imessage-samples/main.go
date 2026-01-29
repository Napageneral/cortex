package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Napageneral/cortex/internal/config"
)

type eventRow struct {
	ID           string
	Timestamp    time.Time
	Sender       string
	Content      string
	ContentTypes string
	MetadataJSON sql.NullString
	ReplyTo      sql.NullString
	Members      sql.NullString
	Attachments  sql.NullString
}

func main() {
	dbPath, err := getCortexDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving cortex.db: %v\n", err)
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening cortex.db: %v\n", err)
		os.Exit(2)
	}
	defer db.Close()

	samples := []struct {
		label string
		query string
	}{
		{
			label: "Membership Join",
			query: `
				SELECT id, thread_id, timestamp
				FROM events
				WHERE channel = 'imessage'
				  AND content_types LIKE '%"membership"%'
				  AND metadata_json LIKE '%"action":"added"%'
				  AND metadata_json LIKE '%"other_contact_id"%'
				ORDER BY timestamp DESC
				LIMIT 1
			`,
		},
		{
			label: "Membership Leave",
			query: `
				SELECT id, thread_id, timestamp
				FROM events
				WHERE channel = 'imessage'
				  AND content_types LIKE '%"membership"%'
				  AND metadata_json LIKE '%"action":"removed"%'
				  AND metadata_json LIKE '%"other_contact_id"%'
				ORDER BY timestamp DESC
				LIMIT 1
			`,
		},
		{
			label: "Reaction",
			query: `
				SELECT id, thread_id, timestamp
				FROM events
				WHERE channel = 'imessage'
				  AND content_types LIKE '%"reaction"%'
				  AND reply_to IS NOT NULL AND reply_to != ''
				ORDER BY timestamp DESC
				LIMIT 1
			`,
		},
		{
			label: "Image Attachment",
			query: `
				SELECT e.id, e.thread_id, e.timestamp
				FROM events e
				JOIN attachments a ON a.event_id = e.id
				WHERE e.channel = 'imessage'
				  AND a.media_type = 'image'
				ORDER BY e.timestamp DESC
				LIMIT 1
			`,
		},
	}

	for _, sample := range samples {
		var eventID, threadID string
		var timestamp int64
		if err := db.QueryRow(sample.query).Scan(&eventID, &threadID, &timestamp); err != nil {
			fmt.Printf("=== %s ===\n", sample.label)
			fmt.Println("No matching event found.\n")
			continue
		}

		events, err := fetchWindowEvents(db, threadID, timestamp, 15, 15)
		if err != nil {
			fmt.Printf("=== %s ===\n", sample.label)
			fmt.Printf("Error fetching window: %v\n\n", err)
			continue
		}

		threadName, channel, isGroup := getThreadInfo(db, threadID)
		participants := deriveParticipants(events)

		// Fix thread name if it's a GUID, phone, or email - fall back to participant names
		if looksLikeChatGUID(threadName) || threadName == "" || (!isGroup && looksLikePhoneOrEmail(threadName)) {
			resolved := resolveThreadNameFromParticipants(db, threadID, participants, isGroup)
			if resolved != "" {
				threadName = resolved
			}
		}

		threadType := "group"
		if !isGroup {
			threadType = "1:1"
		}

		fmt.Printf("=== %s ===\n", sample.label)
		fmt.Printf("<EPISODE_CONTEXT>\n")
		if threadName != "" {
			fmt.Printf("Thread: %s (%s, %s)\n", threadName, channel, threadType)
		} else {
			fmt.Printf("Thread: (%s, %s)\n", channel, threadType)
		}
		if len(participants) > 0 {
			fmt.Printf("Participants: %s\n", strings.Join(participants, ", "))
		}
		fmt.Printf("</EPISODE_CONTEXT>\n\n")

		fmt.Printf("<MESSAGES>\n")
		fmt.Print(encodeEvents(db, events))
		fmt.Printf("</MESSAGES>\n\n")
	}
}

func getCortexDBPath() (string, error) {
	dataDir, err := config.GetDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "cortex.db"), nil
}

func fetchWindowEvents(db *sql.DB, threadID string, centerTS int64, before, after int) ([]eventRow, error) {
	beforeEvents, err := fetchEvents(db, threadID, centerTS, before, true)
	if err != nil {
		return nil, err
	}
	afterEvents, err := fetchEvents(db, threadID, centerTS, after, false)
	if err != nil {
		return nil, err
	}

	// reverse beforeEvents to chronological order
	for i, j := 0, len(beforeEvents)-1; i < j; i, j = i+1, j-1 {
		beforeEvents[i], beforeEvents[j] = beforeEvents[j], beforeEvents[i]
	}

	events := append(beforeEvents, afterEvents...)
	return events, nil
}

func fetchEvents(db *sql.DB, threadID string, centerTS int64, limit int, includeCenter bool) ([]eventRow, error) {
	operator := "<"
	order := "DESC"
	if includeCenter {
		operator = "<="
	}
	if !includeCenter {
		operator = ">"
		order = "ASC"
	}

	query := fmt.Sprintf(`
		SELECT
			e.id,
			e.content,
			e.timestamp,
			e.content_types,
			e.metadata_json,
			e.reply_to,
			COALESCE(p.canonical_name, p.display_name, c.display_name,
				(SELECT ci.value FROM contact_identifiers ci 
				 WHERE ci.contact_id = c.id AND ci.type IN ('phone', 'email')
				 ORDER BY CASE ci.type WHEN 'phone' THEN 1 ELSE 2 END LIMIT 1),
				CASE e.direction WHEN 'sent' THEN 'Me' ELSE 'Unknown' END) as sender,
			(
				SELECT GROUP_CONCAT(
					COALESCE(mp.canonical_name, mp.display_name, mc.display_name,
						(SELECT mci.value FROM contact_identifiers mci 
						 WHERE mci.contact_id = mc.id AND mci.type IN ('phone', 'email')
						 ORDER BY CASE mci.type WHEN 'phone' THEN 1 ELSE 2 END LIMIT 1),
						'Unknown'), '|'
				)
				FROM event_participants mem
				LEFT JOIN contacts mc ON mem.contact_id = mc.id
				LEFT JOIN persons mp ON mp.id = (
					SELECT person_id FROM person_contact_links pcl
					WHERE pcl.contact_id = mem.contact_id
					ORDER BY confidence DESC, last_seen_at DESC
					LIMIT 1
				)
				WHERE mem.event_id = e.id AND mem.role = 'member'
			) as members,
			(
				SELECT GROUP_CONCAT(
					CASE
						WHEN a.media_type = 'image' THEN 'image'
						WHEN a.media_type = 'video' THEN 'video'
						WHEN a.media_type = 'audio' THEN 'audio'
						WHEN a.media_type = 'sticker' THEN 'sticker'
						ELSE COALESCE(a.filename, 'file') || '::' || COALESCE(a.mime_type, '')
					END, '|'
				)
				FROM attachments a WHERE a.event_id = e.id
			) as attachments
		FROM events e
		LEFT JOIN event_participants ep ON e.id = ep.event_id AND ep.role = 'sender'
		LEFT JOIN contacts c ON ep.contact_id = c.id
		LEFT JOIN persons p ON p.id = (
			SELECT person_id FROM person_contact_links pcl
			WHERE pcl.contact_id = ep.contact_id
			ORDER BY confidence DESC, last_seen_at DESC
			LIMIT 1
		)
		WHERE e.thread_id = ?
		  AND e.timestamp %s ?
		ORDER BY e.timestamp %s
		LIMIT ?
	`, operator, order)

	rows, err := db.Query(query, threadID, centerTS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventRow
	for rows.Next() {
		var ev eventRow
		var ts int64
		if err := rows.Scan(&ev.ID, &ev.Content, &ts, &ev.ContentTypes, &ev.MetadataJSON, &ev.ReplyTo, &ev.Sender, &ev.Members, &ev.Attachments); err != nil {
			return nil, err
		}
		ev.Timestamp = time.Unix(ts, 0)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeEvents(db *sql.DB, events []eventRow) string {
	var sb strings.Builder
	messageSnippets := map[string]string{}

	for _, ev := range events {
		timestamp := ev.Timestamp.UTC().Format(time.RFC3339)

		if hasContentType(ev.ContentTypes, "reaction") {
			emoji := strings.TrimSpace(ev.Content)
			if emoji == "" {
				continue
			}
			snippet := ""
			if ev.ReplyTo.Valid && ev.ReplyTo.String != "" {
				if candidate, ok := messageSnippets[ev.ReplyTo.String]; ok {
					snippet = candidate
				} else {
					// Fallback: query database for the original message
					snippet = lookupReplyToContent(db, ev.ReplyTo.String)
				}
			}
			if snippet != "" {
				sb.WriteString(fmt.Sprintf("  -> %s %s to \"%s\"\n", ev.Sender, emoji, snippet))
			} else {
				sb.WriteString(fmt.Sprintf("  -> %s reacted %s\n", ev.Sender, emoji))
			}
			continue
		}

		if hasContentType(ev.ContentTypes, "membership") {
			line := formatMembershipLine(db, ev.Sender, ev.MetadataJSON, ev.Members)
			if line != "" {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
			continue
		}

		var parts []string
		if strings.TrimSpace(ev.Content) != "" {
			parts = append(parts, ev.Content)
		}
		if ev.Attachments.Valid && ev.Attachments.String != "" {
			for _, att := range strings.Split(ev.Attachments.String, "|") {
				switch att {
				case "image":
					parts = append(parts, "[Image]")
				case "video":
					parts = append(parts, "[Video]")
				case "audio":
					parts = append(parts, "[Audio]")
				case "sticker":
					parts = append(parts, "[Sticker]")
				default:
					fileName, mimeType := splitAttachmentDescriptor(att)
					if mimeType != "" {
						parts = append(parts, fmt.Sprintf("[Attachment] %s (%s)", fileName, mimeType))
					} else {
						parts = append(parts, fmt.Sprintf("[Attachment] %s", fileName))
					}
				}
			}
		}

		if len(parts) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", timestamp, ev.Sender, strings.Join(parts, " ")))

		snippet := reactionSnippet(ev.Content)
		if snippet != "" {
			messageSnippets[ev.ID] = snippet
		}
	}

	return sb.String()
}

func getThreadInfo(db *sql.DB, threadID string) (string, string, bool) {
	var name, channel sql.NullString
	var isGroup sql.NullInt64
	err := db.QueryRow(`SELECT name, channel, is_group FROM threads WHERE id = ?`, threadID).Scan(&name, &channel, &isGroup)
	if err != nil {
		// Fallback if is_group column doesn't exist yet
		_ = db.QueryRow(`SELECT name, channel FROM threads WHERE id = ?`, threadID).Scan(&name, &channel)
	}
	
	threadName := ""
	if name.Valid {
		threadName = name.String
	}
	threadChannel := "unknown"
	if channel.Valid {
		threadChannel = channel.String
	}
	
	// Use is_group from database (from Eve's sync of Apple's style column)
	isGroupBool := isGroup.Valid && isGroup.Int64 == 1
	
	return threadName, threadChannel, isGroupBool
}

// looksLikePhoneOrEmail checks if a string looks like a phone number or email
func looksLikePhoneOrEmail(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Check for phone-like patterns
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "1") {
		digits := 0
		for _, r := range s {
			if r >= '0' && r <= '9' {
				digits++
			}
		}
		if digits >= 10 {
			return true
		}
	}
	// Check for email
	if strings.Contains(s, "@") {
		return true
	}
	return false
}

func deriveParticipants(events []eventRow) []string {
	seen := make(map[string]struct{})
	for _, ev := range events {
		name := strings.TrimSpace(ev.Sender)
		if name != "" && name != "Unknown" && name != "Me" {
			seen[name] = struct{}{}
		}
		for _, member := range splitPipeList(ev.Members) {
			seen[member] = struct{}{}
		}
	}
	participants := make([]string, 0, len(seen))
	for name := range seen {
		participants = append(participants, name)
	}
	sort.Strings(participants)
	return participants
}

func hasContentType(contentTypesJSON, target string) bool {
	if contentTypesJSON == "" {
		return false
	}
	return strings.Contains(contentTypesJSON, target)
}

func formatMembershipLine(db *sql.DB, actor string, metadataJSON sql.NullString, members sql.NullString) string {
	action := parseMembershipAction(metadataJSON)
	memberNames := splitPipeList(members)

	// If no member names from event_participants, try to get from metadata_json
	if len(memberNames) == 0 && metadataJSON.Valid {
		memberName := lookupMemberFromMetadata(db, metadataJSON.String)
		if memberName != "" {
			memberNames = []string{memberName}
		}
	}

	actor = strings.TrimSpace(actor)
	if actor == "Unknown" {
		actor = ""
	}
	memberList := strings.Join(memberNames, ", ")

	switch action {
	case "added":
		if memberList == "" {
			return "-> unknown member joined"
		}
		if actor != "" {
			return fmt.Sprintf("-> %s added %s", actor, memberList)
		}
		return fmt.Sprintf("-> %s joined", memberList)
	case "removed":
		if memberList == "" {
			return "-> unknown member left"
		}
		if actor != "" && actor != memberList {
			return fmt.Sprintf("-> %s removed %s", actor, memberList)
		}
		return fmt.Sprintf("-> %s left", memberList)
	default:
		if memberList == "" {
			return ""
		}
		return fmt.Sprintf("-> membership update: %s", memberList)
	}
}

func parseMembershipAction(metadataJSON sql.NullString) string {
	if !metadataJSON.Valid || metadataJSON.String == "" {
		return "unknown"
	}
	if strings.Contains(metadataJSON.String, `"action":"added"`) {
		return "added"
	}
	if strings.Contains(metadataJSON.String, `"action":"removed"`) {
		return "removed"
	}
	return "unknown"
}

func splitPipeList(values sql.NullString) []string {
	if !values.Valid || values.String == "" {
		return nil
	}
	raw := strings.Split(values.String, "|")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" && item != "Unknown" {
			out = append(out, item)
		}
	}
	return out
}

func splitAttachmentDescriptor(att string) (string, string) {
	parts := strings.SplitN(att, "::", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(att), ""
}

func reactionSnippet(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	const maxRunes = 80
	runes := []rune(trimmed)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return trimmed
}

// looksLikeChatGUID returns true if the string looks like an iMessage chat GUID
func looksLikeChatGUID(s string) bool {
	if strings.HasPrefix(s, "chat") && len(s) > 10 {
		suffix := s[4:]
		for _, r := range suffix {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	return false
}

// resolveThreadNameFromParticipants generates a thread name from participant names
func resolveThreadNameFromParticipants(db *sql.DB, threadID string, participants []string, isGroup bool) string {
	// For 1:1 chats, use the other participant's name
	if !isGroup {
		// Filter out "Me", "Tyler Brandt", and phone numbers to find the other person
		for _, p := range participants {
			p = strings.TrimSpace(p)
			if p == "" || p == "Me" || p == "Unknown" {
				continue
			}
			if strings.Contains(strings.ToLower(p), "tyler") {
				continue
			}
			// Skip if it looks like a phone number
			if looksLikePhoneOrEmail(p) {
				continue
			}
			return p
		}
		// If we only have phone numbers, return the first participant that isn't Tyler
		for _, p := range participants {
			p = strings.TrimSpace(p)
			if p != "" && p != "Me" && p != "Unknown" && !strings.Contains(strings.ToLower(p), "tyler") {
				return p
			}
		}
		if len(participants) > 0 {
			return participants[0]
		}
	}

	// For group chats with no name, try to get participant names from the thread
	if isGroup {
		// Query thread participants if we don't have them
		if len(participants) == 0 {
			rows, err := db.Query(`
				SELECT DISTINCT COALESCE(p.canonical_name, p.display_name, c.display_name,
					(SELECT ci.value FROM contact_identifiers ci 
					 WHERE ci.contact_id = c.id AND ci.type IN ('phone', 'email')
					 ORDER BY CASE ci.type WHEN 'phone' THEN 1 ELSE 2 END LIMIT 1))
				FROM event_participants ep
				JOIN events e ON ep.event_id = e.id
				LEFT JOIN contacts c ON ep.contact_id = c.id
				LEFT JOIN persons p ON p.id = (
					SELECT person_id FROM person_contact_links pcl
					WHERE pcl.contact_id = ep.contact_id
					ORDER BY confidence DESC, last_seen_at DESC
					LIMIT 1
				)
				WHERE e.thread_id = ? AND ep.role = 'sender'
				LIMIT 10
			`, threadID)
			if err == nil {
				defer rows.Close()
				for rows.Next() {
					var name sql.NullString
					if rows.Scan(&name) == nil && name.Valid && name.String != "" {
						participants = append(participants, name.String)
					}
				}
			}
		}

		if len(participants) > 0 {
			// Return up to 3 participants joined
			if len(participants) > 3 {
				return strings.Join(participants[:3], ", ") + "..."
			}
			return strings.Join(participants, ", ")
		}
	}

	return ""
}

// lookupReplyToContent looks up the content of a message by event ID
func lookupReplyToContent(db *sql.DB, eventID string) string {
	var content sql.NullString
	err := db.QueryRow(`SELECT content FROM events WHERE id = ?`, eventID).Scan(&content)
	if err != nil || !content.Valid {
		return ""
	}
	return reactionSnippet(content.String)
}

// lookupMemberFromMetadata extracts other_contact_id from metadata and looks up the name
func lookupMemberFromMetadata(db *sql.DB, metadataJSON string) string {
	// Parse other_contact_id from JSON
	// Format: {"action":"added","other_handle_id":123,"other_contact_id":"uuid-here"}
	var contactID string
	
	// Simple extraction - find "other_contact_id":"..."
	idx := strings.Index(metadataJSON, `"other_contact_id":"`)
	if idx == -1 {
		return ""
	}
	start := idx + len(`"other_contact_id":"`)
	end := strings.Index(metadataJSON[start:], `"`)
	if end == -1 {
		return ""
	}
	contactID = metadataJSON[start : start+end]
	
	if contactID == "" {
		return ""
	}
	
	// Look up contact name
	var name sql.NullString
	err := db.QueryRow(`
		SELECT COALESCE(p.canonical_name, p.display_name, c.display_name,
			(SELECT ci.value FROM contact_identifiers ci 
			 WHERE ci.contact_id = c.id AND ci.type IN ('phone', 'email')
			 ORDER BY CASE ci.type WHEN 'phone' THEN 1 ELSE 2 END LIMIT 1))
		FROM contacts c
		LEFT JOIN persons p ON p.id = (
			SELECT person_id FROM person_contact_links pcl
			WHERE pcl.contact_id = c.id
			ORDER BY confidence DESC, last_seen_at DESC
			LIMIT 1
		)
		WHERE c.id = ?
	`, contactID).Scan(&name)
	
	if err != nil || !name.Valid {
		return ""
	}
	return name.String
}
