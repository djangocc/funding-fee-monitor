package clocksync

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

type Syncer struct {
	mu      sync.RWMutex
	offsets map[string]time.Duration // exchange name → offset (server - local)
	client  *http.Client
	stop    chan struct{}
}

func New() *Syncer {
	return &Syncer{
		offsets: make(map[string]time.Duration),
		client:  &http.Client{Timeout: 5 * time.Second},
		stop:    make(chan struct{}),
	}
}

// Offset returns the clock offset for an exchange.
// offset = server_time - local_time
// To get "server now": time.Now().Add(offset)
func (s *Syncer) Offset(exchange string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offsets[exchange]
}

// RealAge returns the true age of a tick, corrected for clock skew.
// age = (local_now + offset) - exchange_timestamp
func (s *Syncer) RealAge(exchange string, exchangeTimestamp time.Time) time.Duration {
	offset := s.Offset(exchange)
	serverNow := time.Now().Add(offset)
	return serverNow.Sub(exchangeTimestamp)
}

// Start begins periodic sync for all configured exchanges.
func (s *Syncer) Start(interval time.Duration) {
	s.syncAll()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.syncAll()
			case <-s.stop:
				return
			}
		}
	}()
}

func (s *Syncer) Stop() {
	close(s.stop)
}

type exchangeTimeEndpoint struct {
	name string
	url  string
}

var endpoints = []exchangeTimeEndpoint{
	{"binance", "https://fapi.binance.com/fapi/v1/time"},
	{"aster", "https://fapi.asterdex.com/fapi/v3/time"},
	{"okx", "https://www.okx.com/api/v5/public/time"},
}

func (s *Syncer) syncAll() {
	for _, ep := range endpoints {
		offset, err := s.measure(ep)
		if err != nil {
			log.Printf("[clocksync] %s: %v", ep.name, err)
			continue
		}
		s.mu.Lock()
		old := s.offsets[ep.name]
		s.offsets[ep.name] = offset
		s.mu.Unlock()
		if abs(offset-old) > time.Millisecond {
			log.Printf("[clocksync] %s: offset=%dms (was %dms)", ep.name, offset.Milliseconds(), old.Milliseconds())
		}
	}
}

// measure does 5 round trips, picks the one with smallest RTT.
func (s *Syncer) measure(ep exchangeTimeEndpoint) (time.Duration, error) {
	type sample struct {
		rtt    time.Duration
		offset time.Duration
	}

	var samples []sample
	for i := 0; i < 5; i++ {
		t1 := time.Now()
		serverMs, err := s.fetchServerTime(ep)
		if err != nil {
			return 0, err
		}
		t4 := time.Now()

		rtt := t4.Sub(t1)
		serverTime := time.UnixMilli(serverMs)
		midpoint := t1.Add(rtt / 2)
		offset := serverTime.Sub(midpoint)

		samples = append(samples, sample{rtt: rtt, offset: offset})
	}

	sort.Slice(samples, func(i, j int) bool {
		return samples[i].rtt < samples[j].rtt
	})

	return samples[0].offset, nil
}

func (s *Syncer) fetchServerTime(ep exchangeTimeEndpoint) (int64, error) {
	resp, err := s.client.Get(ep.url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	if ep.name == "okx" {
		var result struct {
			Data []struct {
				Ts string `json:"ts"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return 0, fmt.Errorf("parse: %w", err)
		}
		if len(result.Data) == 0 {
			return 0, fmt.Errorf("empty response")
		}
		return strconv.ParseInt(result.Data[0].Ts, 10, 64)
	}

	// Binance / Aster: {"serverTime": 1234567890123}
	var result struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("parse: %w", err)
	}
	return result.ServerTime, nil
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return time.Duration(math.Abs(float64(d)))
	}
	return d
}
