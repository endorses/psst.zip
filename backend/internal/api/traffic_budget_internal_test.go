package api

import (
	"context"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"testing"
	"time"
)

func TestTrafficPacerBoundedBurstCancellationAndDirections(t *testing.T) {
	var upload, download trafficPacer
	ctx := context.Background()
	if err := upload.wait(ctx, database.TrafficLeaseBytes, 1); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := upload.wait(canceled, 1, 1); err == nil {
		t.Fatal("exceeded one-lease burst")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation delayed")
	}
	if err := download.wait(ctx, database.TrafficLeaseBytes, 1); err != nil {
		t.Fatal(err)
	}
	upload.refund(1)
	if err := upload.wait(ctx, 1, 1); err != nil {
		t.Fatal(err)
	}
}
func TestTrafficPolicyConcurrencyAppliesWhileBudgetOff(t *testing.T) {
	a := newStreamAdmission(config.Config{MaxActiveStreams: 64, MaxStreamsPerAccount: 64, MaxStreamsPerIP: 64, MaxStreamsPerTransfer: 64, MaxStreamsPerSlot: 64})
	p := database.DefaultTrafficPolicy()
	p.MaxActiveStreams = 2
	p.MaxStreamsPerAccount = 1
	release, ok := a.acquire("owner", "ip", "transfer", "slot", p)
	if !ok {
		t.Fatal("first denied")
	}
	defer release()
	if _, ok = a.acquire("owner", "otherip", "othertransfer", "otherslot", p); ok {
		t.Fatal("account limit ignored while enforcement off")
	}
	second, ok := a.acquire("other", "otherip", "othertransfer", "otherslot", p)
	if !ok {
		t.Fatal("second denied")
	}
	defer second()
	if _, ok = a.acquire("third", "thirdip", "thirdtransfer", "thirdslot", p); ok {
		t.Fatal("global limit ignored")
	}
}
