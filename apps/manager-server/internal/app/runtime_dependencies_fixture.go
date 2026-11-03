//go:build cpa_entry_fixture

package app

// This explicitly tagged acceptance build runs the real main/lifecycle. Only
// external BrowserSkill/enrollment/page and scheduler time use loopback RPC.
// It is not a release artifact or evidence of a real account/parser mapping.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/fullrefresh"
)

type entryFixture struct {
	base, token string
	client      *http.Client
	mu          sync.Mutex
	now         time.Time
	revision    int64
	ticks       chan time.Time
	once        sync.Once
}

func (f *entryFixture) call(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.base+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("entry fixture dependency unavailable")
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
}

func (f *entryFixture) updateClock() (bool, error) {
	var clock struct {
		NowMS    int64
		Revision int64
	}
	if err := f.call(context.Background(), "/fixture/clock", nil, &clock); err != nil {
		return false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	changed := clock.Revision != f.revision
	f.now, f.revision = time.UnixMilli(clock.NowMS), clock.Revision
	return changed, nil
}

func (f *entryFixture) Now() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now }
func (f *entryFixture) Ticks() <-chan time.Time {
	f.once.Do(func() {
		go func() {
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			defer close(f.ticks)
			for range ticker.C {
				changed, err := f.updateClock()
				if err != nil {
					return
				}
				if changed {
					select {
					case f.ticks <- f.Now():
					default:
					}
				}
			}
		}()
	})
	return f.ticks
}

func managementRuntimeDependencies() (balance.WebReader, fullrefresh.Clock) {
	base, token := os.Getenv("CPAMP_ENTRY_FIXTURE_URL"), os.Getenv("CPAMP_ENTRY_FIXTURE_TOKEN")
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(token) < 16 {
		panic("tagged entry fixture requires an explicit authenticated loopback dependency")
	}
	f := &entryFixture{base: base, token: token, client: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("fixture redirect forbidden") }}, ticks: make(chan time.Time, 8)}
	if _, err := f.updateClock(); err != nil {
		panic(fmt.Sprintf("initialize entry fixture clock: %v", err))
	}
	reader := balance.NewBrowserSkill(balance.BrowserSkillOptions{
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			var result struct {
				Output []byte
				Error  string
			}
			err := f.call(ctx, "/fixture/command", args, &result)
			if err != nil {
				return nil, err
			}
			if result.Error != "" {
				return result.Output, errors.New("controlled external command failed")
			}
			return result.Output, nil
		},
		Enrollment: func(q balance.WebRequest) (balance.WebEnrollment, bool) {
			var e balance.WebEnrollment
			err := f.call(context.Background(), "/fixture/enrollment", q, &e)
			// A synthetic parser cannot assert real-browser evidence, even in
			// this explicitly tagged binary. The driver still verifies the full
			// source, authority and all verification flags before any command.
			return e, err == nil && e.EvidenceKind == "controlled_fixture"
		},
		Rules: map[string]func([]byte) (balance.WebPage, error){"synthetic-observed-page-v1": func(raw []byte) (balance.WebPage, error) {
			var page balance.WebPage
			err := json.Unmarshal(raw, &page)
			return page, err
		}},
	})
	return reader, f
}
