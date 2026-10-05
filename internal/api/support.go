package api

import (
	"bufio"
	"container/list"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// --- Eventos de resolución (asíncronos) ---

// Event es una resolución registrada para métricas y auditoría. No incluye el texto
// de la dirección (Apéndice H: logs mínimos).
type Event struct {
	Time              time.Time `json:"time"`
	Client            string    `json:"client"`
	ExternalID        string    `json:"external_id,omitempty"`
	Decision          string    `json:"decision"`
	ResolutionType    string    `json:"resolution_type,omitempty"`
	PrecisionLevel    string    `json:"precision_level,omitempty"`
	Score             float64   `json:"score"`
	ProcessingUS      int64     `json:"processing_us"`
	DatasetVersion    string    `json:"dataset_version"`
	NormalizerVersion string    `json:"normalizer_version"`
}

// EventBuffer encola eventos y los escribe en segundo plano como NDJSON. Push nunca
// bloquea: si el buffer está lleno, el evento se descarta y se cuenta.
type EventBuffer struct {
	ch      chan Event
	dropped atomic.Int64
	done    chan struct{}
}

// NewEventBuffer escribe los eventos en path (se agrega al final del archivo).
func NewEventBuffer(path string, size int) (*EventBuffer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	b := &EventBuffer{ch: make(chan Event, size), done: make(chan struct{})}
	go func() {
		defer close(b.done)
		w := bufio.NewWriter(f)
		enc := json.NewEncoder(w)
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case e, ok := <-b.ch:
				if !ok {
					w.Flush()
					f.Close()
					return
				}
				_ = enc.Encode(e)
			case <-tick.C:
				w.Flush()
			}
		}
	}()
	return b, nil
}

// Push encola un evento sin bloquear. Un buffer nil no hace nada.
func (b *EventBuffer) Push(e Event) {
	if b == nil {
		return
	}
	select {
	case b.ch <- e:
	default:
		b.dropped.Add(1)
	}
}

// Dropped devuelve cuántos eventos se descartaron por buffer lleno.
func (b *EventBuffer) Dropped() int64 {
	if b == nil {
		return 0
	}
	return b.dropped.Load()
}

// Close vacía el buffer y cierra el archivo.
func (b *EventBuffer) Close() {
	if b == nil {
		return
	}
	close(b.ch)
	<-b.done
}

// --- Límite de solicitudes por cliente (token bucket) ---

type limiter struct {
	rate    float64
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate float64) *limiter { return &limiter{rate: rate, buckets: map[string]*bucket{}} }

func (l *limiter) allow(client string) bool {
	if l.rate <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.buckets[client]
	burst := l.rate * 2
	if b == nil {
		b = &bucket{tokens: burst, last: now}
		l.buckets[client] = b
	}
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// --- Caché de idempotencia (LRU) ---

type idemCache struct {
	mu    sync.Mutex
	size  int
	ll    *list.List
	items map[string]*list.Element
}

type idemEntry struct {
	key  string
	body []byte
}

func newIdemCache(size int) *idemCache {
	return &idemCache{size: size, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *idemCache) get(k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*idemEntry).body, true
	}
	return nil, false
}

func (c *idemCache) put(k string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[k]; ok {
		e.Value.(*idemEntry).body = body
		c.ll.MoveToFront(e)
		return
	}
	c.items[k] = c.ll.PushFront(&idemEntry{k, body})
	if c.ll.Len() > c.size {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.items, last.Value.(*idemEntry).key)
	}
}

// --- Métricas en formato de texto de Prometheus ---

// latencyBuckets en milisegundos.
var latencyBuckets = []float64{1, 2, 5, 10, 20, 50, 100, 250, 500, 1000}

// Metrics acumula contadores y un histograma de latencia por ruta.
type Metrics struct {
	mu        sync.Mutex
	requests  map[string]int64 // ruta|código
	decisions map[string]int64 // decisión|precisión
	hist      map[string][]int64
	sum       map[string]float64
	count     map[string]int64
}

// NewMetrics crea un registro vacío.
func NewMetrics() *Metrics {
	return &Metrics{requests: map[string]int64{}, decisions: map[string]int64{}, hist: map[string][]int64{},
		sum: map[string]float64{}, count: map[string]int64{}}
}

func (m *Metrics) request(route string, status int, d time.Duration) {
	ms := float64(d.Microseconds()) / 1000
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests[fmt.Sprintf("%s|%d", route, status)]++
	h := m.hist[route]
	if h == nil {
		h = make([]int64, len(latencyBuckets))
		m.hist[route] = h
	}
	for i, b := range latencyBuckets {
		if ms <= b {
			h[i]++
		}
	}
	m.sum[route] += ms
	m.count[route]++
}

func (m *Metrics) decision(decision, precision string) {
	m.mu.Lock()
	m.decisions[decision+"|"+precision]++
	m.mu.Unlock()
}

// ServeHTTP escribe las métricas en formato de texto de Prometheus.
func (m *Metrics) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	b.WriteString("# TYPE addrsvc_requests_total counter\n")
	for _, k := range sortedKeys(m.requests) {
		p := strings.SplitN(k, "|", 2)
		fmt.Fprintf(&b, "addrsvc_requests_total{route=%q,code=%q} %d\n", p[0], p[1], m.requests[k])
	}
	b.WriteString("# TYPE addrsvc_decisions_total counter\n")
	for _, k := range sortedKeys(m.decisions) {
		p := strings.SplitN(k, "|", 2)
		fmt.Fprintf(&b, "addrsvc_decisions_total{decision=%q,precision=%q} %d\n", p[0], p[1], m.decisions[k])
	}
	b.WriteString("# TYPE addrsvc_request_duration_ms histogram\n")
	routes := make([]string, 0, len(m.hist))
	for r := range m.hist {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	for _, r := range routes {
		for i, le := range latencyBuckets {
			fmt.Fprintf(&b, "addrsvc_request_duration_ms_bucket{route=%q,le=\"%g\"} %d\n", r, le, m.hist[r][i])
		}
		fmt.Fprintf(&b, "addrsvc_request_duration_ms_bucket{route=%q,le=\"+Inf\"} %d\n", r, m.count[r])
		fmt.Fprintf(&b, "addrsvc_request_duration_ms_sum{route=%q} %g\n", r, m.sum[r])
		fmt.Fprintf(&b, "addrsvc_request_duration_ms_count{route=%q} %d\n", r, m.count[r])
	}
	_, _ = w.Write([]byte(b.String()))
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
