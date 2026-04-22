package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

const (
	earthRadiusMeters = 6371000.0
	stepMeters        = 10.0

	// Mais ou menos na praça Coronel Pedro Osório
	startLat = -31.770687426923516
	startLon = -52.34135057529372
)

type Position struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
	Step      int       `json:"step"`
}

type Tracker struct {
	mu       sync.RWMutex
	position Position
	clients  map[chan Position]struct{}
}

func NewTracker() *Tracker {
	return &Tracker{
		position: Position{
			Latitude:  startLat,
			Longitude: startLon,
			Timestamp: time.Now(),
			Step:      0,
		},
		clients: make(map[chan Position]struct{}),
	}
}

func (t *Tracker) GetPosition() Position {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.position
}

func (t *Tracker) SetPosition(p Position) {
	t.mu.Lock()
	t.position = p

	clients := make([]chan Position, 0, len(t.clients))
	for ch := range t.clients {
		clients = append(clients, ch)
	}
	t.mu.Unlock()

	for _, ch := range clients {
		select {
		case ch <- p:
		default:
			// cliente lento, ignora este envio
		}
	}
}

func (t *Tracker) Subscribe() chan Position {
	ch := make(chan Position, 8)

	t.mu.Lock()
	t.clients[ch] = struct{}{}
	t.mu.Unlock()

	return ch
}

func (t *Tracker) Unsubscribe(ch chan Position) {
	t.mu.Lock()
	delete(t.clients, ch)
	t.mu.Unlock()
	close(ch)
}

// movePoint desloca um ponto geográfico por certa distância em uma direção dada.
// Fórmula de navegação sobre esfera.
func movePoint(latDeg, lonDeg, distanceMeters, bearingRad float64) (float64, float64) {
	lat1 := degreesToRadians(latDeg)
	lon1 := degreesToRadians(lonDeg)
	angularDistance := distanceMeters / earthRadiusMeters

	lat2 := math.Asin(
		math.Sin(lat1)*math.Cos(angularDistance) +
			math.Cos(lat1)*math.Sin(angularDistance)*math.Cos(bearingRad),
	)

	lon2 := lon1 + math.Atan2(
		math.Sin(bearingRad)*math.Sin(angularDistance)*math.Cos(lat1),
		math.Cos(angularDistance)-math.Sin(lat1)*math.Sin(lat2),
	)

	return radiansToDegrees(lat2), normalizeLongitude(radiansToDegrees(lon2))
}

func degreesToRadians(d float64) float64 {
	return d * math.Pi / 180.0
}

func radiansToDegrees(r float64) float64 {
	return r * 180.0 / math.Pi
}

func normalizeLongitude(lon float64) float64 {
	for lon > 180.0 {
		lon -= 360.0
	}
	for lon < -180.0 {
		lon += 360.0
	}
	return lon
}

func startSimulation(tracker *Tracker) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		// intervalo aproximado de 5s entre os movimentos
		delay := time.Duration(4+rng.Float64()*2) * time.Second
		time.Sleep(delay)

		current := tracker.GetPosition()
		bearing := rng.Float64() * 2 * math.Pi

		newLat, newLon := movePoint(current.Latitude, current.Longitude, stepMeters, bearing)

		next := Position{
			Latitude:  newLat,
			Longitude: newLon,
			Timestamp: time.Now(),
			Step:      current.Step + 1,
		}

		tracker.SetPosition(next)

		log.Printf(
			"step=%d lat=%.8f lon=%.8f",
			next.Step,
			next.Latitude,
			next.Longitude,
		)
	}
}

func positionHandler(tracker *Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")

		pos := tracker.GetPosition()
		if err := json.NewEncoder(w).Encode(pos); err != nil {
			http.Error(w, "erro ao serializar posição", http.StatusInternalServerError)
			return
		}
	}
}

func sseHandler(tracker *Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming não suportado", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Accel-Buffering", "no")

		ch := tracker.Subscribe()
		defer tracker.Unsubscribe(ch)

		// envia a posição atual imediatamente
		initial := tracker.GetPosition()
		initialJSON, _ := json.Marshal(initial)
		fmt.Fprintf(w, "data: %s\n\n", initialJSON)
		flusher.Flush()

		ctx := r.Context()

		for {
			select {
			case <-ctx.Done():
				return
			case pos := <-ch:
				payload, err := json.Marshal(pos)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}

func main() {
	tracker := NewTracker()
	tracker2 := NewTracker()

	go startSimulation(tracker)
	time.Sleep(10 * time.Nanosecond)
	go startSimulation(tracker2)

	http.HandleFunc("/position", positionHandler(tracker))
	http.HandleFunc("/stream", sseHandler(tracker))

	http.HandleFunc("/position2", positionHandler(tracker2))
	http.HandleFunc("/stream2", sseHandler(tracker2))

	// SERVIR FRONTEND
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/static/index.html")
	})
	fs := http.FileServer(http.Dir("./web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	log.Println("Servidor em http://localhost:8080")
	log.Println("GET /position -> posição atual em JSON")
	log.Println("GET /stream   -> stream SSE com atualizações")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
