package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stuffam/drawbridge/internal/store"
	"github.com/stuffam/drawbridge/internal/views"
)

// A dashboard that can show a value and can't add up a list reads the total over a range, and the
// token it has is enough.
func TestTrafficTotal(t *testing.T) {
	svc := newService(t)
	srv := newServer(t, svc)
	newBrowser(t, srv).expect(http.StatusUnauthorized, "GET", "/api/traffic/total", nil)
	b, _ := loggedIn(t, svc, srv)
	ctx := context.Background()
	phone, _, err := svc.AddClient(ctx, "phone")
	if err != nil {
		t.Fatal(err)
	}
	laptop, _, err := svc.AddClient(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	// Half an hour ago: inside 1h, and settled.
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	if err := svc.Store.InsertTraffic(ctx, []store.TrafficSample{
		{ClientID: phone.ID, Resolution: store.ResolutionRaw, BucketStart: t0, RxBytes: 100, TxBytes: 50},
		{ClientID: laptop.ID, Resolution: store.ResolutionRaw, BucketStart: t0, RxBytes: 10, TxBytes: 5},
		{ClientID: phone.ID, Resolution: store.ResolutionRaw, BucketStart: t0.Add(time.Minute), RxBytes: 200, TxBytes: 75},
		// Three hours ago: outside 1h, inside 12h.
		{ClientID: laptop.ID, Resolution: store.ResolutionRaw, BucketStart: t0.Add(-150 * time.Minute), RxBytes: 1000, TxBytes: 500},
	}); err != nil {
		t.Fatal(err)
	}

	var total views.TrafficTotalView
	b.expect(http.StatusOK, "GET", "/api/traffic/total?range=1h", nil).decode(t, &total)
	if total.Range != "1h" || total.ReceiveBytes != 310 || total.SendBytes != 130 {
		t.Fatalf("1h: %+v, want 310 received and 130 sent", total)
	}
	if got := total.Until.Sub(total.Since); got != time.Hour || total.Until.After(time.Now()) || time.Since(total.Until) > 2*time.Minute {
		t.Errorf("1h window: %v to %v", total.Since, total.Until)
	}

	// No range means 24h, and says so, so a widget's reader knows what the number is.
	b.expect(http.StatusOK, "GET", "/api/traffic/total", nil).decode(t, &total)
	if total.Range != "24h" || total.ReceiveBytes != 1310 || total.SendBytes != 630 || total.Until.Sub(total.Since) != 24*time.Hour {
		t.Fatalf("the default: %+v, want 24h, 1310 received, 630 sent", total)
	}
	b.expect(http.StatusOK, "GET", "/api/traffic/total?range=12h", nil).decode(t, &total)
	if total.Range != "12h" || total.ReceiveBytes != 1310 {
		t.Fatalf("12h: %+v", total)
	}
	// It adds what /api/traffic lists.
	var series views.TrafficSamplesView
	b.expect(http.StatusOK, "GET", "/api/traffic?range=12h", nil).decode(t, &series)
	var rx, tx int64
	for _, s := range series.Samples {
		rx += s.ReceiveBytes
		tx += s.SendBytes
	}
	if rx != total.ReceiveBytes || tx != total.SendBytes {
		t.Errorf("the series adds up to %d and %d, the total says %d and %d", rx, tx, total.ReceiveBytes, total.SendBytes)
	}

	// The live range is served from memory, and nothing has polled.
	b.expect(http.StatusOK, "GET", "/api/traffic/total?range=1m", nil).decode(t, &total)
	if total.Range != "1m" || total.ReceiveBytes != 0 || total.Until.Sub(total.Since) != time.Minute {
		t.Errorf("1m: %+v", total)
	}
	// The hourly ranges answer too (what's in them depends on the hour the test runs in).
	for _, r := range []string{"7d", "30d", "90d"} {
		b.expect(http.StatusOK, "GET", "/api/traffic/total?range="+r, nil).decode(t, &total)
		if total.Range != r || total.Until.Sub(total.Since) <= 24*time.Hour {
			t.Errorf("%s: %+v", r, total)
		}
	}

	r := b.expect(http.StatusBadRequest, "GET", "/api/traffic/total?range=30m", nil)
	if !strings.Contains(r.errorText(), "range must be one of") {
		t.Errorf("a bad range: %q", r.errorText())
	}
}

// What the token reads from the status and the total is what the admin's session reads.
func TestTokenReadsTheTotalsAndTheStatusCounters(t *testing.T) {
	f := newTokenFixture(t)
	ctx := context.Background()
	t0 := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	if err := f.svc.Store.InsertTraffic(ctx, []store.TrafficSample{
		{ClientID: f.clientID, Resolution: store.ResolutionRaw, BucketStart: t0, RxBytes: 1234, TxBytes: 5678},
	}); err != nil {
		t.Fatal(err)
	}
	var byToken, byCookie views.TrafficTotalView
	f.with(t, f.secret, "GET", "/api/traffic/total?range=1h").decode(t, &byToken)
	f.admin.expect(http.StatusOK, "GET", "/api/traffic/total?range=1h", nil).decode(t, &byCookie)
	if byToken != byCookie || byToken.ReceiveBytes != 1234 || byToken.SendBytes != 5678 {
		t.Errorf("by token %+v, by cookie %+v", byToken, byCookie)
	}

	// The status has the counters as plain top-level numbers, which is all a widget can read.
	r := f.with(t, f.secret, "GET", "/api/server/status")
	for _, key := range []string{`"receive_bytes":`, `"send_bytes":`} {
		if !strings.Contains(string(r.body), key) {
			t.Errorf("the status %s lacks %s", r.body, key)
		}
	}
}
