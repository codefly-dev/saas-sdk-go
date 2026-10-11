package datasource_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/codefly-dev/saas-sdk-go/datasource"
)

// This example is compile-only: gw and ctx come from the serving runtime.
func Example_invoke() {
	var gw datasource.Gateway
	var ctx context.Context
	ds := datasource.New(gw)
	result, err := ds.Invoke(ctx, "org-id", "source-id", "list_invoices",
		map[string]any{"limit": 20}, datasource.WithDeadline(time.Now().Add(5*time.Second)))
	if errors.Is(err, datasource.ErrOutcomeUnknown) || errors.Is(err, datasource.ErrOperationDeclarationRemoved) {
		// Keep the original request values (including the person's Work Context)
		// while replacing its expired cancellation/deadline for this lookup.
		lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		receipt, lookupErr := ds.Lookup(lookupCtx, "org-id", "source-id", result.Receipt.EffectID)
		if errors.Is(lookupErr, datasource.ErrOutcomeUnknown) {
			fmt.Println("outcome still unknown", receipt.EffectID)
			return // no repeated effect, and no newly minted ID
		}
		if errors.Is(lookupErr, datasource.ErrEffectNotFound) {
			fmt.Println("effect not found; an explicit same-ID invoke is safe", receipt.EffectID)
			return
		}
		if lookupErr != nil {
			fmt.Println(lookupErr)
			return
		}
		fmt.Println(receipt.Status, string(receipt.Output))
		return
	}
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(result.Output), result.Receipt.EffectID)
}
