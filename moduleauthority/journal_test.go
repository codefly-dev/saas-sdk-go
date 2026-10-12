package moduleauthority

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/codefly-dev/saas-sdk-go/gen/saas/accounts/v1"
)

const journalType = "example.recorded.v1"

var journalEpoch = time.Date(2026, 10, 12, 9, 30, 0, 0, time.UTC)

// journalEntry is a committed entry of the fake host's journal.
func journalEntry(t *testing.T, seq int64, organization, kind string) *v1.ModuleJournalEntry {
	t.Helper()
	data, err := structpb.NewStruct(map[string]any{"actor": "person-" + fmt.Sprint(seq), "count": float64(seq)})
	if err != nil {
		t.Fatal(err)
	}
	return &v1.ModuleJournalEntry{
		Seq: seq, EventId: fmt.Sprintf("event-%d", seq), Type: kind, OrganizationId: organization,
		OccurredAt: timestamppb.New(journalEpoch.Add(time.Duration(seq) * time.Second)), Data: data,
	}
}

func (a *fakeAuthority) commit(entries ...*v1.ModuleJournalEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.journal = append(a.journal, entries...)
}

func (a *fakeAuthority) setCrossTenant(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.crossTenant = v
}

func (a *fakeAuthority) holdResumeAt(seq int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.journalNext = &seq
}

func TestReadJournalReturnsAPageAndItsResumePoint(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	h.authority.commit(
		journalEntry(t, 1, testTenant, journalType),
		journalEntry(t, 2, "", journalType),                // an entry with no organization
		journalEntry(t, 3, testTenant, "example.other.v1"), // a type the reader did not ask for
		journalEntry(t, 4, testOwner, journalType),
	)

	page, err := client.ReadJournal(t.Context(), 0, 10, []string{journalType})
	if err != nil {
		t.Fatal(err)
	}
	if page.NextAfterSeq != 4 {
		t.Fatalf("NextAfterSeq = %d, want 4", page.NextAfterSeq)
	}
	want := []JournalEntry{
		{Seq: 1, EventID: "event-1", Type: journalType, OrganizationID: testTenant, OccurredAt: journalEpoch.Add(time.Second), Data: map[string]any{"actor": "person-1", "count": float64(1)}},
		{Seq: 2, EventID: "event-2", Type: journalType, OrganizationID: "", OccurredAt: journalEpoch.Add(2 * time.Second), Data: map[string]any{"actor": "person-2", "count": float64(2)}},
		{Seq: 4, EventID: "event-4", Type: journalType, OrganizationID: testOwner, OccurredAt: journalEpoch.Add(4 * time.Second), Data: map[string]any{"actor": "person-4", "count": float64(4)}},
	}
	if !reflect.DeepEqual(page.Entries, want) {
		t.Fatalf("entries = %+v\nwant     %+v", page.Entries, want)
	}

	// The request on the wire, and the authority it travelled under.
	calls := h.authority.snapshot()
	if len(calls) != 1 {
		t.Fatalf("authority calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.method != "/saas.accounts.v1.ModuleCapabilitiesService/ReadJournal" {
		t.Fatalf("method = %q", call.method)
	}
	if len(call.internalToken) != 1 || call.internalToken[0] != testInternalToken ||
		len(call.workContext) != 1 || call.workContext[0] != "module.token1" || len(call.authorization) != 0 {
		t.Fatal("wrong authority carriers")
	}
	if call.journal.GetAfterSeq() != 0 || call.journal.GetLimit() != 10 || !reflect.DeepEqual(call.journal.GetTypes(), []string{journalType}) {
		t.Fatalf("request = %+v", call.journal)
	}
	h.assertNoStrayPaths(t)
}

func TestReadJournalResumesFromTheReturnedPointAndHonoursTheLimit(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for seq := int64(1); seq <= 5; seq++ {
		h.authority.commit(journalEntry(t, seq, testTenant, journalType))
	}
	var seen []int64
	after := int64(0)
	for pages := 0; ; pages++ {
		page, err := client.ReadJournal(t.Context(), after, 2, []string{journalType})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) == 0 {
			if page.NextAfterSeq != after {
				t.Fatalf("an empty page moved the resume point from %d to %d", after, page.NextAfterSeq)
			}
			break
		}
		if len(page.Entries) > 2 {
			t.Fatalf("page of %d entries for a limit of 2", len(page.Entries))
		}
		for _, e := range page.Entries {
			seen = append(seen, e.Seq)
		}
		after = page.NextAfterSeq
		if pages > 5 {
			t.Fatal("the reader does not reach the end of the journal")
		}
	}
	if !reflect.DeepEqual(seen, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("seen = %v", seen)
	}
}

// The resume point is the host's to name. A reader must use it as given, even
// when it is behind the last entry the page carried.
func TestReadJournalReportsTheHostsResumePointNotTheLastSeq(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	h.authority.commit(journalEntry(t, 1, testTenant, journalType), journalEntry(t, 3, testTenant, journalType))
	h.authority.holdResumeAt(1)
	page, err := client.ReadJournal(t.Context(), 0, 0, []string{journalType})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.NextAfterSeq != 1 {
		t.Fatalf("entries=%d next=%d, want 2 entries and next=1", len(page.Entries), page.NextAfterSeq)
	}
}

func TestReadJournalAnEntryWithoutATimestampOrDataDecodes(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	h.authority.commit(&v1.ModuleJournalEntry{Seq: 7, EventId: "event-7", Type: journalType})
	page, err := client.ReadJournal(t.Context(), 0, 1, []string{journalType})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || !page.Entries[0].OccurredAt.IsZero() || len(page.Entries[0].Data) != 0 {
		t.Fatalf("entries = %+v", page.Entries)
	}
}

// A refusal keeps the host's own code, like every other authority call: a
// caller that is not a cross-tenant module principal is PermissionDenied, and
// nothing is returned that could be mistaken for an empty journal.
func TestReadJournalRefusalKeepsTheHostsCode(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	h.authority.commit(journalEntry(t, 1, testTenant, journalType))
	h.authority.setCrossTenant(false)
	page, err := client.ReadJournal(t.Context(), 0, 10, []string{journalType})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
	if len(page.Entries) != 0 || page.NextAfterSeq != 0 {
		t.Fatalf("a refused read returned %+v", page)
	}
	if calls := h.authority.snapshot(); len(calls) != 1 {
		t.Fatalf("a refusal that is not a rejected capability was retried: %d calls", len(calls))
	}
}

func TestReadJournalRefreshesOnlyARejectedModuleCapability(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	h.authority.commit(journalEntry(t, 1, testTenant, journalType))
	if _, err := client.ModuleWorkContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	h.gateway.revoke("module.token1")
	page, err := client.ReadJournal(t.Context(), 0, 10, []string{journalType})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	calls := h.authority.snapshot()
	if len(calls) != 2 || calls[0].workContext[0] != "module.token1" || calls[1].workContext[0] != "module.token2" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestReadJournalRefusesAMalformedReadBeforeAnyCall(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for name, read := range map[string]struct {
		after int64
		limit int32
		types []string
	}{
		"negative position": {after: -1, limit: 10, types: []string{journalType}},
		"negative limit":    {limit: -1, types: []string{journalType}},
		"limit over 500":    {limit: 501, types: []string{journalType}},
		"no types":          {limit: 10},
		"empty type":        {limit: 10, types: []string{journalType, ""}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.ReadJournal(t.Context(), read.after, read.limit, read.types); !errors.Is(err, ErrInvalidJournalRead) {
				t.Fatalf("err = %v, want ErrInvalidJournalRead", err)
			}
		})
	}
	if calls := h.authority.snapshot(); len(calls) != 0 {
		t.Fatalf("a malformed read reached the host %d times", len(calls))
	}
	// The bounds themselves are valid.
	for _, limit := range []int32{0, 1, MaxJournalPage} {
		if _, err := client.ReadJournal(t.Context(), 0, limit, []string{journalType}); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}

func TestReadJournalDoesNotAliasTheCallersTypes(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	types := []string{journalType}
	if _, err := client.ReadJournal(t.Context(), 0, 1, types); err != nil {
		t.Fatal(err)
	}
	types[0] = "changed"
	if got := h.authority.snapshot()[0].journal.GetTypes(); got[0] != journalType {
		t.Fatalf("request types followed the caller's slice: %v", got)
	}
}

func TestReadJournalNeedsTheAuthoritySeam(t *testing.T) {
	h := newHost(t)
	seams := h.seams()
	seams.Authority = Authority{}
	client, err := New(seams, moduleCredentials)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if _, err := client.ReadJournal(t.Context(), 0, 1, []string{journalType}); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("err = %v, want ErrNoAuthority", err)
	}
}
