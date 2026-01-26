package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Napageneral/cortex/internal/contacts"
	"github.com/google/uuid"
)

// ContactsAdapter syncs Google Contacts via gogcli.
//
// Primary purpose: build the identity graph (email <-> phone <-> names) so
// cortex can unify iMessage and Gmail participants.
type ContactsAdapter struct {
	name    string
	account string
	opts    ContactsAdapterOptions
}

type ContactsAdapterOptions struct {
	Workers int
	QPS     float64
}

func (o ContactsAdapterOptions) withDefaults() ContactsAdapterOptions {
	if o.Workers <= 0 {
		o.Workers = 64 // People API allows 100 QPS, saturate it
	}
	if o.QPS <= 0 {
		o.QPS = 80 // People API quota is 100/s, push close to limit
	}
	return o
}

func NewContactsAdapter(name, account string, opts ...ContactsAdapterOptions) (*ContactsAdapter, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("adapter instance name is required for contacts adapter")
	}
	if strings.TrimSpace(account) == "" {
		return nil, fmt.Errorf("account email is required for contacts adapter")
	}
	if _, err := exec.LookPath("gog"); err != nil {
		return nil, fmt.Errorf("gogcli (gog) not found in PATH. Install with: brew install steipete/tap/gogcli")
	}
	var o ContactsAdapterOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	return &ContactsAdapter{name: name, account: account, opts: o.withDefaults()}, nil
}

func (c *ContactsAdapter) Name() string { return c.name }

type gogContactsListResponse struct {
	Contacts      []gogContact `json:"contacts"`
	NextPageToken string       `json:"nextPageToken"`
}

type gogContact struct {
	Resource string `json:"resource"`
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
}

func normalizePhone(s string) string {
	return contacts.NormalizeIdentifier(s, "phone")
}

func (c *ContactsAdapter) fetchContacts(ctx context.Context) ([]gogContact, error) {
	var out []gogContact

	// Primary contacts
	page := ""
	for {
		args := []string{"contacts", "list", "--json", "--max", "500", "--account", c.account}
		if page != "" {
			args = append(args, "--page", page)
		}
		cmd := exec.CommandContext(ctx, "gog", args...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("gog contacts list failed: %w (output: %s)", err, string(b))
		}
		var resp gogContactsListResponse
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse contacts json: %w", err)
		}
		out = append(out, resp.Contacts...)
		if resp.NextPageToken == "" || len(resp.Contacts) == 0 {
			break
		}
		page = resp.NextPageToken
	}

	// Other contacts (often contains emails inferred from interactions)
	page = ""
	for {
		args := []string{"contacts", "other", "list", "--json", "--max", "500", "--account", c.account}
		if page != "" {
			args = append(args, "--page", page)
		}
		cmd := exec.CommandContext(ctx, "gog", args...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("gog contacts other list failed: %w (output: %s)", err, string(b))
		}
		var resp gogContactsListResponse
		if err := json.Unmarshal(b, &resp); err != nil {
			return nil, fmt.Errorf("failed to parse other contacts json: %w", err)
		}
		out = append(out, resp.Contacts...)
		if resp.NextPageToken == "" || len(resp.Contacts) == 0 {
			break
		}
		page = resp.NextPageToken
	}

	return out, nil
}

type gogContactGetResponse struct {
	Found   bool           `json:"found"`
	Contact gogContactFull `json:"contact"`
}

type gogContactFull struct {
	ResourceName string `json:"resourceName"`
	Names        []struct {
		DisplayName string `json:"displayName"`
	} `json:"names"`
	EmailAddresses []struct {
		Value string `json:"value"`
	} `json:"emailAddresses"`
	PhoneNumbers []struct {
		CanonicalForm string `json:"canonicalForm"`
		Value         string `json:"value"`
	} `json:"phoneNumbers"`
}

func (c *ContactsAdapter) fetchContactDetails(ctx context.Context, resource string) (name string, emails []string, phones []string, ok bool, err error) {
	if !strings.HasPrefix(resource, "people/") {
		return "", nil, nil, false, nil
	}
	cmd := exec.CommandContext(ctx, "gog", "contacts", "get", resource, "--json", "--account", c.account)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, nil, false, fmt.Errorf("gog contacts get failed (%s): %w (output: %s)", resource, err, string(b))
	}
	var resp gogContactGetResponse
	if err := json.Unmarshal(b, &resp); err != nil {
		return "", nil, nil, false, fmt.Errorf("failed to parse contacts get json: %w", err)
	}
	// Some implementations return {"found":false}.
	if resp.Found == false && resp.Contact.ResourceName == "" {
		return "", nil, nil, false, nil
	}
	if len(resp.Contact.Names) > 0 {
		name = strings.TrimSpace(resp.Contact.Names[0].DisplayName)
	}
	seenE := map[string]struct{}{}
	for _, e := range resp.Contact.EmailAddresses {
		v := strings.TrimSpace(strings.ToLower(e.Value))
		if v == "" {
			continue
		}
		if _, ok := seenE[v]; ok {
			continue
		}
		seenE[v] = struct{}{}
		emails = append(emails, v)
	}
	seenP := map[string]struct{}{}
	for _, p := range resp.Contact.PhoneNumbers {
		v := p.CanonicalForm
		if strings.TrimSpace(v) == "" {
			v = p.Value
		}
		v = normalizePhone(v)
		if v == "" {
			continue
		}
		if _, ok := seenP[v]; ok {
			continue
		}
		seenP[v] = struct{}{}
		phones = append(phones, v)
	}
	return name, emails, phones, true, nil
}

func (c *ContactsAdapter) getPersonByIdentity(db *sql.DB, channel string, ident string) (personID string, isMe bool, ok bool, err error) {
	var pid string
	if err := db.QueryRow(`SELECT person_id FROM identities WHERE channel = ? AND identifier = ?`, channel, ident).Scan(&pid); err != nil {
		if err == sql.ErrNoRows {
			return "", false, false, nil
		}
		return "", false, false, err
	}
	var me int
	if err := db.QueryRow(`SELECT is_me FROM persons WHERE id = ?`, pid).Scan(&me); err != nil {
		return "", false, false, err
	}
	return pid, me == 1, true, nil
}

func (c *ContactsAdapter) createPerson(db *sql.DB, canonical string) (string, error) {
	id := uuid.New().String()
	now := time.Now().Unix()
	_, err := db.Exec(`
		INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?)
	`, id, canonical, now, now)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (c *ContactsAdapter) addIdentity(db *sql.DB, personID, channel, ident string) error {
	ident = strings.TrimSpace(ident)
	if ident == "" {
		return nil
	}
	now := time.Now().Unix()
	_, err := db.Exec(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO NOTHING
	`, uuid.New().String(), personID, channel, ident, now)
	return err
}

// contactWithDetails holds a contact after detail fetch
type contactWithDetails struct {
	Resource string
	Name     string
	Emails   []string
	Phones   []string
}

func (c *ContactsAdapter) Sync(ctx context.Context, cortexDB *sql.DB, full bool) (SyncResult, error) {
	start := time.Now()
	res := SyncResult{Perf: map[string]string{}}
	_ = full // contacts sync is effectively always "full" (idempotent)

	// Phase 1: List all contacts (paginated, fast)
	tList := time.Now()
	gogContacts, err := c.fetchContacts(ctx)
	if err != nil {
		return res, err
	}
	res.Perf["list_duration"] = time.Since(tList).String()
	res.Perf["contacts_total"] = fmt.Sprintf("%d", len(gogContacts))

	// Phase 2: Parallel fetch details for people/... contacts
	tDetails := time.Now()
	detailed := c.fetchAllDetailsParallel(ctx, gogContacts)
	res.Perf["details_duration"] = time.Since(tDetails).String()
	res.Perf["details_fetched"] = fmt.Sprintf("%d", len(detailed))

	// Phase 3: Process contacts and link to persons (DB-bound, sequential)
	// We intentionally do NOT wrap the entire run in one transaction to keep
	// writes short and reduce lock contention.
	tProcess := time.Now()
	processed := 0
	linked := 0

	for _, ctc := range detailed {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		default:
		}

		emails := ctc.Emails
		phones := ctc.Phones
		name := ctc.Name

		contactIDs := make([]string, 0, len(emails)+len(phones))
		for _, e := range emails {
			if strings.TrimSpace(e) == "" {
				continue
			}
			contactID, _, err := contacts.GetOrCreateContact(cortexDB, "email", e, name, c.Name())
			if err != nil {
				return res, err
			}
			contactIDs = append(contactIDs, contactID)
		}
		for _, p := range phones {
			if strings.TrimSpace(p) == "" {
				continue
			}
			contactID, _, err := contacts.GetOrCreateContact(cortexDB, "phone", p, name, c.Name())
			if err != nil {
				return res, err
			}
			contactIDs = append(contactIDs, contactID)
		}

		if len(contactIDs) == 0 {
			continue
		}

		var basePerson string
		for _, cid := range contactIDs {
			pid, err := contacts.GetLinkedPersonID(cortexDB, cid)
			if err != nil {
				return res, err
			}
			if pid != "" {
				basePerson = pid
				break
			}
		}
		if basePerson == "" && contacts.IsMeaningfulPersonName(name) {
			pid, created, err := contacts.EnsurePersonForContact(cortexDB, contactIDs[0], name, "deterministic", 1.0)
			if err != nil {
				return res, err
			}
			if created {
				res.PersonsCreated++
			}
			basePerson = pid
		}

		if basePerson != "" {
			for _, cid := range contactIDs {
				if err := contacts.EnsurePersonContactLink(cortexDB, basePerson, cid, "deterministic", 1.0); err != nil {
					return res, err
				}
				linked++
			}
		}

		processed++
		if processed%500 == 0 {
			fmt.Printf("  Contacts processed: %d/%d\n", processed, len(detailed))
		}
	}

	res.Perf["process_duration"] = time.Since(tProcess).String()
	res.Perf["contacts_processed"] = fmt.Sprintf("%d", processed)
	res.Perf["contacts_linked"] = fmt.Sprintf("%d", linked)
	res.Duration = time.Since(start)
	res.Perf["total"] = res.Duration.String()
	return res, nil
}

// fetchAllDetailsParallel fetches contact details in parallel using worker pool
func (c *ContactsAdapter) fetchAllDetailsParallel(ctx context.Context, contacts []gogContact) []contactWithDetails {
	// Build list of contacts that need detail fetch
	type job struct {
		idx int
		ctc gogContact
	}
	jobs := make(chan job)
	results := make(chan struct {
		idx int
		det contactWithDetails
	})

	// Rate limiter
	interval := time.Duration(float64(time.Second) / c.opts.QPS)
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Workers
	var wg sync.WaitGroup
	for w := 0; w < c.opts.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}

				det := contactWithDetails{
					Resource: j.ctc.Resource,
					Name:     strings.TrimSpace(j.ctc.Name),
				}

				// Fetch full details if people/... resource
				if strings.HasPrefix(j.ctc.Resource, "people/") {
					n2, e2, p2, ok, err := c.fetchContactDetails(ctx, j.ctc.Resource)
					if err == nil && ok {
						if n2 != "" {
							det.Name = n2
						}
						det.Emails = append(det.Emails, e2...)
						det.Phones = append(det.Phones, p2...)
					}
				}

				// Add base email/phone from list response
				if j.ctc.Email != "" {
					det.Emails = append(det.Emails, strings.TrimSpace(strings.ToLower(j.ctc.Email)))
				}
				if j.ctc.Phone != "" {
					det.Phones = append(det.Phones, normalizePhone(j.ctc.Phone))
				}

				// Dedupe
				det.Emails = dedupeStrings(det.Emails, func(s string) string {
					return strings.TrimSpace(strings.ToLower(s))
				})
				det.Phones = dedupeStrings(det.Phones, normalizePhone)

				results <- struct {
					idx int
					det contactWithDetails
				}{idx: j.idx, det: det}
			}
		}()
	}

	// Send jobs
	go func() {
		for i, ctc := range contacts {
			jobs <- job{idx: i, ctc: ctc}
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	// Collect results preserving order
	out := make([]contactWithDetails, len(contacts))
	count := 0
	for r := range results {
		out[r.idx] = r.det
		count++
		if count%200 == 0 {
			fmt.Printf("  Fetched details: %d/%d\n", count, len(contacts))
		}
	}

	// Filter out empty
	filtered := make([]contactWithDetails, 0, len(out))
	for _, d := range out {
		if len(d.Emails) > 0 || len(d.Phones) > 0 {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

func dedupeStrings(s []string, normalize func(string) string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(s))
	for _, v := range s {
		v = normalize(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
