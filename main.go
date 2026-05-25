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
	"database/sql"
	"os"
)

import _ "github.com/mattn/go-sqlite3"

const (
	earthRadiusMeters = 6371000.0
)

// Limites da cidade
const (
	north = -31.67639556064381
	south = -31.782363778608364
	west  = -52.40539684839434
	east  = -52.22725717890395
)

//struct para distribuir as entidades dentro de pelotas
type Center struct {
	Lat    float64
	Lon    float64
	Weight float64
	Spread float64
}

var centers = []Center{
	{-31.754334069624566, -52.37529453247065, 0.11, 0.005}, // duque de caxias
	{-31.72028658259838, -52.34878335194222, 0.11, 0.006}, //tres vendas
	{-31.744009936368936, -52.3885366965394, 0.07, 0.005}, //gotuzzo
	{-31.752819530018023, -52.329905558483816, 0.24, 0.008}, //bento
	{-31.772598986121313, -52.335470989836054, 0.22, 0.006}, //porto
	{-31.76337488187622, -52.23561339476515, 0.03, 0.004}, //laranjal
	{-31.747045781696023, -52.30530635087559, 0.09, 0.003}, // areal
	{-31.758707004704647, -52.26999275000219, 0.01, 0.002}, //recanto de portugal
	{-31.761480182298946, -52.35908500145639, 0.02, 0.003}, // perto do if
	{-31.736555424961292, -52.31339886333048, 0.02, 0.003}, //direita do aeroporto
	{-31.731341236041338, -52.32977869312375, 0.02, 0.003}, //cohab aeroporto
	{-31.735823877882282, -52.34039789524478, 0.02, 0.004}, //rural
	{-31.770075971297242, -52.31672182758014, 0.01, 0.003}, //una
	{-31.76745665133588, -52.35346648070491, 0.02, 0.003}, //if
	{-31.760509378409115, -52.34355651729728, 0.02, 0.003}, //pelotense
}

const numPokemonTypes = 31 //quantidade de sprites de pokemon

type EntityType int

const (
	Player EntityType = iota
	Pokemon
	Pokestop
)

//struct de posicoes
type Position struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
	Step      int       `json:"step"`
}

// struct entity que trata de todas as entidades de forma misturada (com aval do chatgpt)
type Entity struct {
	//todas as entidades tem
	ID   string     `json:"id"`
	Type EntityType `json:"type"`
	Pos  Position   `json:"pos"`
	
	//pokemon
	OriginLat float64
	OriginLon float64
	MaxRadius float64
	PokemonType int `json:"pokemon_type"`
	Active       bool     `json:"active"`

	//player
	Pokedex      []string `json:"pokedex,omitempty"`
	CaptureBoost bool     `json:"capture_boost,omitempty"`
	Path []Position `json:"-"`
	CapturedCount int `json:"-"`
	PokestopCount int `json:"-"`
	
	//pokestop
	PokestopAvailable bool
	CooldownUntil time.Time `json:"-"`

}

//struct que vai guardar todas as entidades
type World struct {
	mu       sync.RWMutex
	entities map[string]*Entity
	clients  map[chan []Entity]struct{}
	nextID int
}

func NewWorld() *World {
	return &World{
		entities: make(map[string]*Entity),
		clients:  make(map[chan []Entity]struct{}),
	}
}

func chooseCenter(rng *rand.Rand) Center {
	r := rng.Float64()
	sum := 0.0

	for _, c := range centers {
		sum += c.Weight
		if r <= sum {
			return c
		}
	}

	return centers[len(centers)-1]
}

func multiCenterPosition(rng *rand.Rand) (float64, float64) {
	c := chooseCenter(rng)

	lat := c.Lat + rng.NormFloat64()*c.Spread
	lon := c.Lon + rng.NormFloat64()*c.Spread

	if lat > north {
		lat = north
	}
	if lat < south {
		lat = south
	}
	if lon > east {
		lon = east
	}
	if lon < west {
		lon = west
	}

	return lat, lon
}


func (w *World) SpawnPokemons(n int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < n; i++ {
		lat, lon := multiCenterPosition(rng)

		id := fmt.Sprintf("pokemon_%d", i)
		w.entities[id] = &Entity{
			ID:   id,
			Type: Pokemon,
			Pos: Position{
				Latitude:  lat,
				Longitude: lon,
				Timestamp: time.Now(),
				Step: 10,
			},
			OriginLat: lat,
			OriginLon: lon,
			MaxRadius: 30,

			PokemonType: rng.Intn(numPokemonTypes),
			Active: true,
			
		}
	}
	w.nextID += n
}

func (w *World) SpawnPlayers(n int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < n; i++ {
		lat, lon := multiCenterPosition(rng)

		id := fmt.Sprintf("player_%d", i)
		w.entities[id] = &Entity{
			ID:   id,
			Type: Player,
			Pos: Position{
				Latitude:  lat,
				Longitude: lon,
				Timestamp: time.Now(),
				Step: 30,
			},
			Active: true,
		}
	}
}

func (w *World) SpawnPokeStops(n int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < n; i++ {
		lat, lon := multiCenterPosition(rng)

		id := fmt.Sprintf("pokestop_%d", i)
		w.entities[id] = &Entity{
			ID:   id,
			Type: Pokestop,
			Pos: Position{
				Latitude:  lat,
				Longitude: lon,
				Timestamp: time.Now(),
				Step: 0,
			},
			Active: true,
			PokestopAvailable: true,
		}
	}
}

func (w *World) spawnRandomPokemon(rng *rand.Rand) {

	if rng.Float64() > 0.8 {
		return
	}
	
	for i := 0; i < 2; i++{
		id := fmt.Sprintf("pokemon_%d", w.nextID)
		w.nextID++

		lat, lon := multiCenterPosition(rng)

		w.entities[id] = &Entity{
			ID:   id,
			Type: Pokemon,
			Pos: Position{
				Latitude:  lat,
				Longitude: lon,
				Timestamp: time.Now(),
			},
			PokemonType: rng.Intn(numPokemonTypes),
			Active: true,
		}

		log.Printf("Novo pokemon spawnado: %s", id)
	}
}



func (w *World) StartSimulation() {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	

	for {
		time.Sleep(3 * time.Second)

		now := time.Now()
		
		w.mu.Lock()

		for _, e := range w.entities {

			switch(e.Type){
			
			case Pokestop:
				if !e.PokestopAvailable{
					if now.After(e.CooldownUntil) {
						e.PokestopAvailable = true
						log.Printf("Pokestop %s reativado", e.ID)
					}
			}

			case Pokemon:
				*e = movePokemon(*e)
			
			case Player:
			
				bearing := rng.Float64() * 2 * math.Pi

				newLat, newLon := movePoint(
					e.Pos.Latitude,
					e.Pos.Longitude,
					float64(e.Pos.Step),
					bearing,
				)

				if newLat > north || newLat < south || newLon > east || newLon < west {
					continue
				}

				e.Pos.Latitude = newLat
				e.Pos.Longitude = newLon
				e.Pos.Timestamp = time.Now()
				e.Path = append(e.Path, e.Pos)
		}
		}

		w.handleInteractions(rng)
		w.spawnRandomPokemon(rng)

		snapshot := make([]Entity, 0, len(w.entities))
		for _, e := range w.entities {
			if e.Active{
				snapshot = append(snapshot, *e)
			}
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

func movePokemon(e Entity) Entity {
	bearing := rand.Float64() * 2 * math.Pi

	newLat, newLon := movePoint(
		e.Pos.Latitude,
		e.Pos.Longitude,
		float64(e.Pos.Step),
		bearing,
	)

	dist := distanceMeters(
		e.OriginLat,
		e.OriginLon,
		newLat,
		newLon,
	)

	
	if dist > e.MaxRadius {
		return e
	}

	e.Pos.Latitude = newLat
	e.Pos.Longitude = newLon

	return e
}

func distanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000.0

	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*
			math.Cos(degreesToRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return R * c
}

func (w *World) handleInteractions(rng *rand.Rand) {
	for _, player := range w.entities {
		interacted := false

		if player.Type != Player || !player.Active {
			continue
		}

		for _, target := range w.entities {

			if interacted {
				break
			}

			if !target.Active {
				continue
			}


			dist := distanceMeters(
				player.Pos.Latitude,
				player.Pos.Longitude,
				target.Pos.Latitude,
				target.Pos.Longitude,
			)

			if dist > 20.0 {
				continue
			}

			switch target.Type {

			case Pokemon:

				chance := 0.4
				if player.CaptureBoost {
					chance = 0.7
					player.CaptureBoost = false
				}

				if rng.Float64() < chance {
					player.Pos.Latitude = target.Pos.Latitude
					player.Pos.Longitude = target.Pos.Longitude
					player.Pokedex = append(player.Pokedex, target.ID)
					player.CapturedCount++
					target.Active = false

					log.Printf("Player %s capturou %s", player.ID, target.ID)
					interacted = true
				}

			case Pokestop:

				if !target.PokestopAvailable {
					continue
				}

				player.Pos.Latitude = target.Pos.Latitude
				player.Pos.Longitude = target.Pos.Longitude

				player.CaptureBoost = true
				target.PokestopAvailable = false
				target.CooldownUntil = time.Now().Add(1 * time.Minute)
				log.Printf("Player %s usou Pokestop %s", player.ID, target.ID)
				player.PokestopCount++
				interacted = true

			}
		}
	}
}


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


func initDB() *sql.DB {
	os.Remove("tracker.db")

	db, err := sql.Open("sqlite3", "tracker.db")
	if err != nil {
		log.Fatal(err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS positions (
	player_id TEXT,
	lat REAL,
	lon REAL,
	timestamp DATETIME
	);
	`

	_, err = db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}

	query2 := `
	CREATE TABLE IF NOT EXISTS players (
		id TEXT PRIMARY KEY,
		total_captures INTEGER,
		total_pokestops INTEGER
	);
	`

	_, err = db.Exec(query2)
	if err != nil {
		log.Fatal(err)
	}

	return db
}

func (w *World) savePlayers(db *sql.DB) {
	w.mu.Lock()

	players := make([]*Entity, 0)

	for _, e := range w.entities {
		if e.Type == Player {
			players = append(players, e)
		}
	}

	w.mu.Unlock()

	tx, err := db.Begin()
	if err != nil {
		log.Println("erro ao iniciar transação:", err)
		return
	}

	for _, e := range players {

		_, err := tx.Exec(`
			INSERT INTO players(id, total_captures, total_pokestops)
			VALUES (?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				total_captures = total_captures + ?,
				total_pokestops = total_pokestops + ?
		`,
			e.ID,
			e.CapturedCount,
			e.PokestopCount,
			e.CapturedCount,
			e.PokestopCount,
		)

		if err != nil {
			log.Println("erro ao salvar player:", err)
		}

		for _, p := range e.Path {
			_, err := tx.Exec(`
				INSERT INTO positions(player_id, lat, lon, timestamp)
				VALUES (?, ?, ?, ?)
			`, e.ID, p.Latitude, p.Longitude, p.Timestamp)
		
			if err != nil {
				log.Println("erro ao salvar posição:", err)
			}
		}

		e.Path = nil
		e.CapturedCount = 0
		e.PokestopCount = 0
	}

	err = tx.Commit()
	if err != nil {
		log.Println("erro no commit:", err)
	}
}

func main() {
	world := NewWorld()

	world.SpawnPokemons(1000)
	world.SpawnPlayers(300)
	world.SpawnPokeStops(150)

	go world.StartSimulation()
	db := initDB()
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			world.savePlayers(db)
			log.Println("Salvou posições no banco")
		}
	}()

	http.HandleFunc("/stream", world.SSEHandler)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/static/index.html")
	})

	fs := http.FileServer(http.Dir("./web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	log.Println("Servidor em http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}