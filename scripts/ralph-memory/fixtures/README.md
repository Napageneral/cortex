# Verification Fixtures — Real Data for Memory System Testing

## Purpose

These fixtures use **real data** from Tyler's communication sources (iMessage, Gmail, AIX) to verify the memory system works correctly. Real data enables better "vibe eval" — you can look at the output and know if it makes sense.

## Directory Structure

```
fixtures/
├── README.md                    # This file
├── imessage/
│   ├── identity-disclosure/     # Someone shares their email/phone
│   │   ├── episode.json
│   │   └── expectations.yaml
│   ├── job-change/              # Employment change with temporal bounds
│   ├── social-relationship/     # Dating/spouse/friend relationships
│   └── shared-identifier/       # Family phone number scenario
├── gmail/
│   ├── newsletter-sender/       # Company identity from emails
│   ├── work-thread/             # Colleague relationships
│   └── event-invitation/        # Calendar event extraction
└── aix/
    ├── personal-info/           # User shares personal details
    ├── project-discussion/      # Project entity extraction
    └── multi-person/            # Multiple people mentioned
```

## Fixture Format

### episode.json

```json
{
  "id": "fixture-imessage-001",
  "source": "imessage",
  "channel": "imessage",
  "thread_id": "chat123456",
  "reference_time": "2026-01-22T10:30:00Z",
  "events": [
    {
      "id": "evt-001",
      "timestamp": "2026-01-22T10:30:00Z",
      "sender": "Casey Adams",
      "sender_identifier": "+1-555-123-4567",
      "content": "My new work email is casey@anthropic.com btw",
      "direction": "inbound"
    }
  ],
  "metadata": {
    "description": "Casey shares their work email in iMessage",
    "coverage_tags": ["identity_disclosure", "email", "self_disclosed"]
  }
}
```

### expectations.yaml

```yaml
# Fixture: Casey shares work email
description: "Casey self-discloses their work email"
source: imessage

entities:
  must_have:
    - name_contains: "Casey"
      entity_type: Person
      
  must_not_have:
    - name: "casey@anthropic.com"  # Emails are aliases, not entities
      entity_type: any

aliases:
  must_have:
    - entity_name_contains: "Casey"
      alias: "casey@anthropic.com"
      alias_type: email
      
relationships:
  must_not_have:
    # Identity relationships go to aliases, not relationships table
    - relation_type: HAS_EMAIL
      
mentions:
  must_have:
    - extracted_fact_contains: "email"
      source_type: self_disclosed
      target_literal: "casey@anthropic.com"
```

## Coverage Matrix

Each fixture should test specific behaviors. Track coverage here:

| Fixture | Entity Types | Relationship Types | Resolution | Temporal | Identity |
|---------|--------------|-------------------|------------|----------|----------|
| imessage/identity-disclosure | Person | HAS_EMAIL | new entity | - | ✓ promote |
| imessage/job-change | Person, Company | WORKS_AT | resolve existing | valid_at, invalid_at | - |
| imessage/social-relationship | Person, Person | DATING, SPOUSE_OF | - | valid_at | - |
| gmail/newsletter-sender | Company | - | new entity | - | - |
| gmail/work-thread | Person, Company | WORKS_AT | resolve | - | - |
| aix/personal-info | Person, Location | BORN_ON, LIVES_IN | - | target_literal date | - |
| aix/project-discussion | Person, Project | BUILDING, WORKING_ON | - | - | - |

## How to Select Real Data

### From iMessage (via eve/imsg)
```bash
# Find messages where someone shares contact info
imsg search "my email" --limit 10
imsg search "my phone" --limit 10
imsg search "started at" --limit 10  # Job changes
```

### From Gmail (via gog)
```bash
# Find emails with identity info
gog gmail search "from:newsletter" --limit 5
gog gmail search "subject:invitation" --limit 5
```

### From AIX (cursor sessions)
```bash
# Look through recent sessions in ~/.aix/sessions/
ls -la ~/.aix/sessions/ | head -20
```

## Anonymization Guidelines

1. **Keep real structure** — Don't change relationship patterns
2. **Change identifiers** — Use fake emails/phones/addresses
3. **Keep names if comfortable** — Or use consistent pseudonyms
4. **Preserve dates** — Real temporal patterns matter

## Adding a New Fixture

1. Find real data that covers a gap in the coverage matrix
2. Create directory: `fixtures/{source}/{scenario-name}/`
3. Create `episode.json` with the episode data
4. Create `expectations.yaml` with assertions
5. Update coverage matrix in this README
6. Run verification harness to validate
