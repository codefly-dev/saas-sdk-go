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

// journalEntry is a committed entry of the fake host's journal. Its position is
// an opaque string to the client; the fake host reads it back for equality.
func journalEntry(t *testing.T, n int, organization, kind string) *v1.ModuleJournalEntry {
	t.Helper()
	data, err := structpb.NewStruct(map[string]any{"actor": "person-" + fmt.Sprint(n), "count": float64(n)})
	if err != nil {
		t.Fatal(err)
	}
	return &v1.ModuleJournalEntry{
		Position: fmt.Sprintf("tx%d.%d", 100+n/2, n), EventId: fmt.Sprintf("event-%d", n), Type: kind, OrganizationId: organization,
		OccurredAt: timestamppb.New(journalEpoch.Add(time.Duration(n) * time.Second)), Data: data,
	}
}

func (a *fakeAuthority) commit(entries ...*v1.ModuleJournalEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.journal = append(a.journal, entries...)
}

// markMachinery records that the entry is the host's delivery machinery for the
// binding (or, with moduleMintMarker, the caller's own module mint).
func (a *fakeAuthority) markMachinery(e *v1.ModuleJournalEntry, binding string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.machinery == nil {
		a.machinery = map[string]string{}
	}
	a.machinery[e.GetEventId()] = binding
}

func (a *fakeAuthority) setCrossTenant(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.crossTenant = v
}

func (a *fakeAuthority) holdResumeAt(position string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.journalNext = &position
}

func TestReadJournalReturnsAPageAndItsResumePoint(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	e1, e2 := journalEntry(t, 1, testTenant, journalType), journalEntry(t, 2, "", journalType) // e2 has no organization
	e3 := journalEntry(t, 3, testTenant, "example.other.v1")                                   // a type the reader did not ask for
	e4 := journalEntry(t, 4, testOwner, journalType)
	h.authority.commit(e1, e2, e3, e4)

	page, err := client.ReadJournal(t.Context(), "", 10, []string{journalType}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextAfter != e4.GetPosition() {
		t.Fatalf("NextAfter = %q, want %q", page.NextAfter, e4.GetPosition())
	}
	want := []JournalEntry{
		{Position: e1.GetPosition(), EventID: "event-1", Type: journalType, OrganizationID: testTenant, OccurredAt: journalEpoch.Add(time.Second), Data: map[string]any{"actor": "person-1", "count": float64(1)}},
		{Position: e2.GetPosition(), EventID: "event-2", Type: journalType, OrganizationID: "", OccurredAt: journalEpoch.Add(2 * time.Second), Data: map[string]any{"actor": "person-2", "count": float64(2)}},
		{Position: e4.GetPosition(), EventID: "event-4", Type: journalType, OrganizationID: testOwner, OccurredAt: journalEpoch.Add(4 * time.Second), Data: map[string]any{"actor": "person-4", "count": float64(4)}},
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
	if call.journal.GetAfter() != "" || call.journal.GetLimit() != 10 || !reflect.DeepEqual(call.journal.GetTypes(), []string{journalType}) ||
		len(call.journal.GetDeliveryBindings()) != 0 {
		t.Fatalf("request = %+v", call.journal)
	}
	h.assertNoStrayPaths(t)
}

func TestReadJournalResumesFromTheReturnedPointAndHonoursTheLimit(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	for n := 1; n <= 5; n++ {
		h.authority.commit(journalEntry(t, n, testTenant, journalType))
	}
	var seen []string
	after := ""
	for pages := 0; ; pages++ {
		page, err := client.ReadJournal(t.Context(), after, 2, []string{journalType}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) == 0 {
			if page.NextAfter != after {
				t.Fatalf("an empty page moved the resume point from %q to %q", after, page.NextAfter)
			}
			break
		}
		if len(page.Entries) > 2 {
			t.Fatalf("page of %d entries for a limit of 2", len(page.Entries))
		}
		for _, e := range page.Entries {
			seen = append(seen, e.EventID)
		}
		after = page.NextAfter
		if pages > 5 {
			t.Fatal("the reader does not reach the end of the journal")
		}
	}
	if !reflect.DeepEqual(seen, []string{"event-1", "event-2", "event-3", "event-4", "event-5"}) {
		t.Fatalf("seen = %v", seen)
	}
}

// The resume point is the host's to name. A reader must use it as given, even
// when it is behind the last entry the page carried.
func TestReadJournalReportsTheHostsResumePointNotTheLastPosition(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	e1, e3 := journalEntry(t, 1, testTenant, journalType), journalEntry(t, 3, testTenant, journalType)
	h.authority.commit(e1, e3)
	h.authority.holdResumeAt(e1.GetPosition())
	page, err := client.ReadJournal(t.Context(), "", 0, []string{journalType}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 2 || page.NextAfter != e1.GetPosition() {
		t.Fatalf("entries=%d next=%q, want 2 entries and next=%q", len(page.Entries), page.NextAfter, e1.GetPosition())
	}
}

// JournalStart reads from the oldest retained entry, as the empty position does,
// and is not any entry's position.
func TestReadJournalAcceptsTheStartPosition(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	e1, e2 := journalEntry(t, 1, testTenant, journalType), journalEntry(t, 2, testTenant, journalType)
	h.authority.commit(e1, e2)
	if JournalStart != "start" {
		t.Fatalf("JournalStart = %q, the literal the host defines is %q", JournalStart, "start")
	}
	page, err := client.ReadJournal(t.Context(), JournalStart, 1, []string{journalType}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Position != e1.GetPosition() || page.NextAfter != e1.GetPosition() {
		t.Fatalf("page = %+v", page)
	}
	if page.Entries[0].Position == JournalStart {
		t.Fatal("an entry carries the start position")
	}
	if got := h.authority.snapshot()[0].journal.GetAfter(); got != JournalStart {
		t.Fatalf("after on the wire = %q", got)
	}
	// And the read resumes from what it returned.
	if page, err = client.ReadJournal(t.Context(), page.NextAfter, 1, []string{journalType}, nil); err != nil || len(page.Entries) != 1 || page.Entries[0].EventID != "event-2" {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
}

// The host marks delivery machinery, for the bindings the read names and for
// the caller's own module mint; the client carries the mark and the bindings
// exactly, and a mark for a binding the read did not name is not set.
func TestReadJournalCarriesTheDeliveryBindingsAndTheHostsMachineryMark(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	real, mint, named, unnamed := journalEntry(t, 1, testTenant, journalType), journalEntry(t, 2, "", journalType),
		journalEntry(t, 3, testTenant, journalType), journalEntry(t, 4, testTenant, journalType)
	h.authority.commit(real, mint, named, unnamed)
	h.authority.markMachinery(mint, moduleMintMarker)
	h.authority.markMachinery(named, "binding-a")
	h.authority.markMachinery(unnamed, "binding-b")

	page, err := client.ReadJournal(t.Context(), "", 0, []string{journalType}, []string{"binding-a", "binding-c"})
	if err != nil {
		t.Fatal(err)
	}
	var got []bool
	for _, e := range page.Entries {
		got = append(got, e.DeliveryMachinery)
	}
	if !reflect.DeepEqual(got, []bool{false, true, true, false}) {
		t.Fatalf("delivery_machinery = %v", got)
	}
	if b := h.authority.snapshot()[0].journal.GetDeliveryBindings(); !reflect.DeepEqual(b, []string{"binding-a", "binding-c"}) {
		t.Fatalf("delivery_bindings on the wire = %v", b)
	}
}

func TestReadJournalAnEntryWithoutATimestampOrDataDecodes(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	h.authority.commit(&v1.ModuleJournalEntry{Position: "p7", EventId: "event-7", Type: journalType})
	page, err := client.ReadJournal(t.Context(), "", 1, []string{journalType}, nil)
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
	page, err := client.ReadJournal(t.Context(), "", 10, []string{journalType}, nil)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("err = %v, want PermissionDenied", err)
	}
	if len(page.Entries) != 0 || page.NextAfter != "" {
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
	page, err := client.ReadJournal(t.Context(), "", 10, []string{journalType}, nil)
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
		limit    int32
		types    []string
		bindings []string
	}{
		"negative limit":       {limit: -1, types: []string{journalType}},
		"limit over 500":       {limit: 501, types: []string{journalType}},
		"no types":             {limit: 10},
		"empty type":           {limit: 10, types: []string{journalType, ""}},
		"empty delivery bound": {limit: 10, types: []string{journalType}, bindings: []string{"binding-a", ""}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.ReadJournal(t.Context(), "", read.limit, read.types, read.bindings); !errors.Is(err, ErrInvalidJournalRead) {
				t.Fatalf("err = %v, want ErrInvalidJournalRead", err)
			}
		})
	}
	if calls := h.authority.snapshot(); len(calls) != 0 {
		t.Fatalf("a malformed read reached the host %d times", len(calls))
	}
	// The bounds themselves are valid.
	for _, limit := range []int32{0, 1, MaxJournalPage} {
		if _, err := client.ReadJournal(t.Context(), "", limit, []string{journalType}, nil); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}

func TestReadJournalDoesNotAliasTheCallersTypes(t *testing.T) {
	h := newHost(t)
	client := h.client(t, moduleCredentials)
	types, bindings := []string{journalType}, []string{"binding-a"}
	if _, err := client.ReadJournal(t.Context(), "", 1, types, bindings); err != nil {
		t.Fatal(err)
	}
	types[0], bindings[0] = "changed", "changed"
	call := h.authority.snapshot()[0].journal
	if call.GetTypes()[0] != journalType || call.GetDeliveryBindings()[0] != "binding-a" {
		t.Fatalf("request followed the caller's slices: %v %v", call.GetTypes(), call.GetDeliveryBindings())
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
	if _, err := client.ReadJournal(t.Context(), "", 1, []string{journalType}, nil); !errors.Is(err, ErrNoAuthority) {
		t.Fatalf("err = %v, want ErrNoAuthority", err)
	}
}
