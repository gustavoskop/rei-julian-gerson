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

	startLat = -31.770687426923516
	startLon = -52.34135057529372
)

type EntityType int

const (
	Player EntityType = iota
	Pokemon
	Pokestop
)

type Position struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
	Step      int       `json:"step"`
}

type Entity struct {
	ID   string     `json:"id"`
	Type EntityType `json:"type"`
	Pos  Position   `json:"pos"`
}

type World struct {
	mu       sync.RWMutex
	entities map[string]*Entity
	clients  map[chan []Entity]struct{}
}

func NewWorld() *World {
	return &World{
		entities: make(map[string]*Entity),
		clients:  make(map[chan []Entity]struct{}),
	}
}

func (w *World) Seed(n int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < n; i++ {
		id := fmt.Sprintf("ent_%d", i)
		t := EntityType(rng.Intn(3))

		w.entities[id] = &Entity{
			ID:   id,
			Type: t,
			Pos: Position{
				Latitude:  startLat + (rng.Float64()-0.5)*0.001,
				Longitude: startLon + (rng.Float64()-0.5)*0.001,
				Timestamp: time.Now(),
				Step:      0,
			},
		}
	}
}

func (w *World) StartSimulation() {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for {
		time.Sleep(2 * time.Second)

		w.mu.Lock()

		for _, e := range w.entities {
			// Pokestop não se move
			if e.Type == Pokestop {
				continue
			}

			bearing := rng.Float64() * 2 * math.Pi

			newLat, newLon := movePoint(
				e.Pos.Latitude,
				e.Pos.Longitude,
				stepMeters,
				bearing,
			)

			e.Pos.Latitude = newLat
			e.Pos.Longitude = newLon
			e.Pos.Timestamp = time.Now()
			e.Pos.Step++
		}

		snapshot := make([]Entity, 0, len(w.entities))
		for _, e := range w.entities {
			snapshot = append(snapshot, *e)
		}

		clients := make([]chan []Entity, 0, len(w.clients))
		for ch := range w.clients {
			clients = append(clients, ch)
		}

		w.mu.Unlock()

		for _, ch := range clients {
			select {
			case ch <- snapshot:
			default:
			}
		}
	}
}

func (w *World) SSEHandler(wr http.ResponseWriter, r *http.Request) {
	flusher, ok := wr.(http.Flusher)
	if !ok {
		http.Error(wr, "Streaming não suportado", 500)
		return
	}

	wr.Header().Set("Content-Type", "text/event-stream")
	wr.Header().Set("Cache-Control", "no-cache")
	wr.Header().Set("Connection", "keep-alive")
	wr.Header().Set("Access-Control-Allow-Origin", "*")

	ch := make(chan []Entity, 8)

	w.mu.Lock()
	w.clients[ch] = struct{}{}
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		delete(w.clients, ch)
		w.mu.Unlock()
		close(ch)
	}()

	for {
		select {
		case data := <-ch:
			payload, _ := json.Marshal(data)
			fmt.Fprintf(wr, "data: %s\n\n", payload)
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

// ================= GEO =================

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
	for lon > 180 {
		lon -= 360
	}
	for lon < -180 {
		lon += 360
	}
	return lon
}

// ================= MAIN =================

func main() {
	world := NewWorld()

	world.Seed(200) // 🔥 quantidade de entidades

	go world.StartSimulation()

	http.HandleFunc("/stream", world.SSEHandler)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/static/index.html")
	})

	fs := http.FileServer(http.Dir("./web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	log.Println("Servidor em http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}