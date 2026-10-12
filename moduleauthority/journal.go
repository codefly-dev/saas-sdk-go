package moduleauthority

import (
	"context"
	"errors"
	"fmt"
	"time"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
	"github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1/accountsv1connect"
)

// ErrInvalidJournalRead is returned before any call for a journal read the host
// would refuse as malformed: a negative position, a page size outside 0..500, or
// no registered type to read.
var ErrInvalidJournalRead = errors.New("module authority: invalid journal read")

// MaxJournalPage is the largest page the host serves. A limit of zero asks for
// the host's default page.
const MaxJournalPage = 500

// JournalEntry is one entry of the host's journal, as the host committed it.
type JournalEntry struct {
	// Seq is the entry's position in the journal. It orders entries; it is not
	// a promise that every lower position is already visible, which is why a
	// reader resumes from JournalPage.NextAfterSeq and never from the last Seq it
	// happened to see.
	Seq     int64
	EventID string
	Type    string
	// OrganizationID is empty for an entry that belongs to no organization.
	OrganizationID string
	OccurredAt     time.Time
	// Data is the entry's payload, decoded from the host's JSON object.
	Data map[string]any
}

// JournalPage is one page of the journal.
type JournalPage struct {
	Entries []JournalEntry
	// NextAfterSeq is where the next read resumes. The host guarantees that a
	// reader resuming here misses no committed entry; resuming from anywhere
	// else carries no such promise. It equals the position the page was asked
	// from when the page is empty.
	NextAfterSeq int64
}

// ReadJournal reads the host's journal forward from afterSeq, for the given
// registered event types, as this module. The host serves it only to a
// cross-tenant module principal; any other caller is refused with the host's
// own code, which is preserved.
//
// afterSeq is exclusive, and zero reads from the oldest retained entry. limit is
// 1..500, and zero means the host's default. types must name at least one
// registered type. A read changes nothing on the host: it is safe to repeat, and
// a reader that resumes from an earlier NextAfterSeq is served the same entries
// again. A rejected module capability is refreshed and retried once, like every
// authority call.
func (c *Client) ReadJournal(ctx context.Context, afterSeq int64, limit int32, types []string) (JournalPage, error) {
	if afterSeq < 0 || limit < 0 || limit > MaxJournalPage || len(types) == 0 {
		return JournalPage{}, ErrInvalidJournalRead
	}
	for _, t := range types {
		if t == "" {
			return JournalPage{}, ErrInvalidJournalRead
		}
	}
	res, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.ReadJournal, &v1.ModuleReadJournalRequest{
		AfterSeq: afterSeq,
		Limit:    limit,
		Types:    append([]string(nil), types...),
	})
	if err != nil {
		return JournalPage{}, fmt.Errorf("module authority: read journal: %w", err)
	}
	page := JournalPage{NextAfterSeq: res.GetNextAfterSeq()}
	for _, e := range res.GetEntries() {
		entry := JournalEntry{
			Seq: e.GetSeq(), EventID: e.GetEventId(), Type: e.GetType(),
			OrganizationID: e.GetOrganizationId(), Data: e.GetData().AsMap(),
		}
		if e.GetOccurredAt() != nil {
			entry.OccurredAt = e.GetOccurredAt().AsTime()
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}
