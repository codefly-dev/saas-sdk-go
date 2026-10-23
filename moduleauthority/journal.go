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
// would refuse as malformed: a page size outside 0..500, no registered type to
// read, or an empty type or delivery binding id.
var ErrInvalidJournalRead = errors.New("module authority: invalid journal read")

// JournalStart is the position that means "before the oldest retained entry".
// The host accepts it wherever it accepts a position (ReadJournal's after, and
// the `from` of the delivery operation) and never returns it as an entry's
// position, so a reader with no position of its own starts a range from it and
// the range still has a position to be identified by. The empty string also
// reads from the oldest retained entry, but it names nothing a caller can keep.
const JournalStart = "start"

// MaxJournalPage is the largest page the host serves. A limit of zero asks for
// the host's default page.
const MaxJournalPage = 500

// JournalEntry is one entry of the host's journal, as the host committed it.
type JournalEntry struct {
	// Position is the entry's place in the journal. It is opaque: a reader
	// stores it and compares it for equality, and never parses it or orders by
	// it. The host orders entries; a reader resumes from JournalPage.NextAfter,
	// not from the last Position it happened to see.
	Position string
	EventID  string
	Type     string
	// OrganizationID is empty for an entry that belongs to no organization.
	OrganizationID string
	OccurredAt     time.Time
	// Data is the entry's payload, decoded from the host's JSON object.
	Data map[string]any
	// DeliveryMachinery is set by the host: the entry records a mint or an
	// exchange for one of the delivery bindings the read named, or the caller's
	// own module work-context mint. A reader that delivers journal entries
	// uses it to tell a page of its own delivery from a page of real activity.
	DeliveryMachinery bool
}

// JournalPage is one page of the journal.
type JournalPage struct {
	Entries []JournalEntry
	// NextAfter is where the next read resumes. It is opaque, like Position. The
	// host guarantees that a reader resuming here misses no committed entry, and
	// never passes an unfinished transaction; resuming from anywhere else
	// carries no such promise. It equals the position the page was asked from
	// when the page is empty.
	NextAfter string
}

// ReadJournal reads the host's journal forward from after, for the given
// registered event types, as this module. The host serves it only to a
// cross-tenant module principal; any other caller is refused with the host's
// own code, which is preserved.
//
// after is an opaque position the host gave, exclusive; JournalStart or the empty
// string reads from the oldest retained entry. limit is 1..500, and zero means the host's
// default. types must name at least one registered type. deliveryBindings are
// the caller's binding ids whose mints and exchanges count as delivery
// machinery: the host sets JournalEntry.DeliveryMachinery on the entries that
// record one of them, and may be empty for a reader that has none. A read
// changes nothing on the host: it is safe to repeat, and a reader that resumes
// from an earlier NextAfter is served the same entries again. A rejected module
// capability is refreshed and retried once, like every authority call.
func (c *Client) ReadJournal(ctx context.Context, after string, limit int32, types, deliveryBindings []string) (JournalPage, error) {
	if limit < 0 || limit > MaxJournalPage || len(types) == 0 {
		return JournalPage{}, ErrInvalidJournalRead
	}
	for _, t := range types {
		if t == "" {
			return JournalPage{}, ErrInvalidJournalRead
		}
	}
	for _, b := range deliveryBindings {
		if b == "" {
			return JournalPage{}, ErrInvalidJournalRead
		}
	}
	res, err := callAsModule(ctx, c, accountsv1connect.ModuleCapabilitiesServiceClient.ReadJournal, &v1.ModuleReadJournalRequest{
		After:            after,
		Limit:            limit,
		Types:            append([]string(nil), types...),
		DeliveryBindings: append([]string(nil), deliveryBindings...),
	})
	if err != nil {
		return JournalPage{}, fmt.Errorf("module authority: read journal: %w", err)
	}
	page := JournalPage{NextAfter: res.GetNextAfter()}
	for _, e := range res.GetEntries() {
		entry := JournalEntry{
			Position: e.GetPosition(), EventID: e.GetEventId(), Type: e.GetType(),
			OrganizationID: e.GetOrganizationId(), Data: e.GetData().AsMap(),
			DeliveryMachinery: e.GetDeliveryMachinery(),
		}
		if e.GetOccurredAt() != nil {
			entry.OccurredAt = e.GetOccurredAt().AsTime()
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}
