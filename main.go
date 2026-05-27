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
	north = -31.70565156128568
	south = -31.782363778608364
	west  = -52.40539684839434
	east  = -52.22725717890395
)

type RawNode struct {
	ID          int     `json:"id"`
	Lat         float64 `json:"y"`
	Lon         float64 `json:"x"`
	StreetCount int     `json:"street_count"`
}

type Node struct {
	ID        string
	Lat       float64
	Lon       float64
	Neighbors []string
}

type Graph struct {
	Nodes map[string]*Node
}

//struct para distribuir as entidades dentro de pelotas
type Center struct {
	Lat    float64
	Lon    float64
	Weight float64
	Spread float64
}

var centers = []Center{
	{-31.754334069624566, -52.37529453247065, 0.1, 0.005}, // duque de caxias
	{-31.72028658259838, -52.34878335194222, 0.11, 0.006}, //tres vendas
	{-31.744009936368936, -52.3885366965394, 0.07, 0.005}, //gotuzzo
	{-31.752819530018023, -52.329905558483816, 0.23, 0.008}, //bento
	{-31.772598986121313, -52.335470989836054, 0.22, 0.006}, //porto
	{-31.76337488187622, -52.23561339476515, 0.03, 0.005}, //laranjal
	{-31.747045781696023, -52.30530635087559, 0.08, 0.005}, // areal
	{-31.758707004704647, -52.26999275000219, 0.01, 0.002}, //recanto de portugal
	{-31.761480182298946, -52.35908500145639, 0.02, 0.003}, // perto do if
	{-31.736555424961292, -52.31339886333048, 0.02, 0.003}, //direita do aeroporto
	{-31.731341236041338, -52.32977869312375, 0.02, 0.003}, //cohab aeroporto
	{-31.735823877882282, -52.34039789524478, 0.02, 0.004}, //rural
	{-31.770075971297242, -52.31672182758014, 0.01, 0.003}, //una
	{-31.76745665133588, -52.35346648070491, 0.02, 0.003}, //if
	{-31.760509378409115, -52.34355651729728, 0.02, 0.003}, //pelotense
	{-31.72629553620015, -52.31054641182939, 0.01, 0.003},
	{-31.721503380599103, -52.302480861258374, 0.01, 0.003},
}

const numPokemonTypes = 31 //quantidade de sprites de pokemon

//struct de posicoes
type Position struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
	Step      int       `json:"step"`
}

// struct entity que trata de todas as entidades
type BaseEntity struct {
	ID   string   `json:"id"`
	Pos  Position `json:"pos"`
	Active bool   `json:"active"`
}

type Player struct {
	BaseEntity

	CaptureBoost bool
	Path         []Position

	CapturedCount int
	PokestopCount int

	// movimento em grafo
	CurrentNode string
	LastNode    string
	TargetNode  string
	Progress    float64
	Speed       float64
}

type Pokemon struct {
	BaseEntity

	OriginLat  float64
	OriginLon  float64
	MaxRadius  float64
	PokemonType int
}

type Pokestop struct {
	BaseEntity

	PokestopAvailable bool
	CooldownUntil     time.Time
}

//struct que vai guardar todas as entidades
type World struct {
	mu sync.RWMutex

	players   map[string]*Player
	pokemons  map[string]*Pokemon
	pokestops map[string]*Pokestop

	clients map[chan Snapshot]struct{}
	nextID  int

	graph *Graph
}

// struct para enviar os dados ao frontend
type EntityDTO struct {
	ID   string   `json:"id"`
	Type string   `json:"type"`
	Pos  Position `json:"pos"`

	PokemonType int  `json:"pokemon_type"`
	PokestopAvailable bool `json:"pokestop_available,omitempty"`
}

// separa as entidades no frontend
type Snapshot struct {
	Players   []EntityDTO `json:"players"`
	Pokemons  []EntityDTO `json:"pokemons"`
	Pokestops []EntityDTO `json:"pokestops"`
}

//cria maps para entidades e os clients para o frontend
func NewWorld() *World {
	return &World{
		players:   make(map[string]*Player),
		pokemons:  make(map[string]*Pokemon),
		pokestops: make(map[string]*Pokestop),

		clients: make(map[chan Snapshot]struct{}),
	}
}

//funções para escolher o spawn baseado nos pesos de centers
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

// os spawns são parecidos, escolhem o spawn baseado nos pesos do vetor centers e atribuem seus respectivos campos
func (w *World) SpawnPokemons(n int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	for i := 0; i < n; i++ {
		id := fmt.Sprintf("pokemon_%d", w.nextID)
		w.nextID++

		lat, lon := multiCenterPosition(rng)

		w.pokemons[id] = &Pokemon{
			BaseEntity: BaseEntity{
				ID: id,
				Pos: Position{
					Latitude: lat,
					Longitude: lon,
					Timestamp: time.Now(),
					Step: 10,
				},
				Active: true,
			},
			OriginLat: lat,
			OriginLon: lon,
			MaxRadius: 30,
			PokemonType: rng.Intn(numPokemonTypes),
		}
	}
}

func (w *World) SpawnPlayersFromGraph(n int, g *Graph) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	keys := make([]string, 0, len(g.Nodes))
	for k := range g.Nodes {
		keys = append(keys, k)
	}

	for i := 0; i < n; i++ {
		nodeID := keys[rng.Intn(len(keys))]
		node := g.Nodes[nodeID]

		id := fmt.Sprintf("player_%d", i)

		w.players[id] = &Player{
			BaseEntity: BaseEntity{
				ID:          id,
				Pos: Position{
					Latitude:  node.Lat,
					Longitude: node.Lon,
					Timestamp: time.Now(),
				},
				Active: true,	
			},
			CurrentNode: nodeID,
			Speed: 25.0, // metros por tick
		}
	}
}

func isInsideCity(lat, lon float64) bool {
	return lat <= north &&
		lat >= south &&
		lon >= west &&
		lon <= east
}

func (w *World) SpawnPokeStops(n int) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for i := 0; i < n; i++ {
		lat, lon := multiCenterPosition(rng)

		id := fmt.Sprintf("pokestop_%d", i)
		w.pokestops[id] = &Pokestop{
			BaseEntity: BaseEntity{
				ID:   id,
				Pos: Position{
					Latitude:  lat,
					Longitude: lon,
					Timestamp: time.Now(),
					Step: 0, // não se move
				},
				Active: true,
			},
			PokestopAvailable: true,
		}
	}
}

// a cada "tick", tem 80% de chance de spawnar 2 pokemons novos
func (w *World) spawnRandomPokemon(rng *rand.Rand) {

	if rng.Float64() > 0.8 {
		return
	}
	
	for i := 0; i < 2; i++{
		id := fmt.Sprintf("pokemon_%d", w.nextID)
		w.nextID++

		lat, lon := multiCenterPosition(rng)

		w.pokemons[id] = &Pokemon{
			BaseEntity: BaseEntity{		
				ID:   id,
				Pos: Position{
					Latitude:  lat,
					Longitude: lon,
					Timestamp: time.Now(),
				},
				Active: true,
			},
			PokemonType: rng.Intn(numPokemonTypes),
		}

		log.Printf("Novo pokemon spawnado: %s", id)
	}
}


// loop principal
func (w *World) StartSimulation() {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	

	for {
		time.Sleep(3 * time.Second) // tick de 3 segundos

		now := time.Now()
		
		w.mu.Lock() // lock para as goroutines não executarem esse campo ao mesmo tempo

		for _, e := range w.players {
			movePlayerGraphSmooth(e, w.graph, rng)
		}
			
		for _, e := range w.pokestops{

			if !e.PokestopAvailable{
				if now.After(e.CooldownUntil) { // caso o pokestop já tenha sido usado e o cooldown acabou, ele volta a ficar disponível
					e.PokestopAvailable = true
					log.Printf("Pokestop %s reativado", e.ID)
				}
			}
		}
			for _, p := range w.pokemons {
				movePokemon(p)
			}		
		
		w.handleInteractions(rng) //verifica para todos os jogadores se pode interagir com pokestop ou pokemon
		w.spawnRandomPokemon(rng)

		snapshot := Snapshot{
			Players:   make([]EntityDTO, 0, len(w.players)),
			Pokemons:  make([]EntityDTO, 0, len(w.pokemons)),
			Pokestops: make([]EntityDTO, 0, len(w.pokestops)),
		}
		for _, p := range w.players {
			if !p.Active {
				continue
			}
		
			snapshot.Players = append(snapshot.Players, EntityDTO{
				ID:   p.ID,
				Type: "player",
				Pos:  p.Pos,
			})
		}
		for _, p := range w.pokemons {
			if !p.Active {
				continue
			}
		
			snapshot.Pokemons = append(snapshot.Pokemons, EntityDTO{
				ID:          p.ID,
				Type:        "pokemon",
				Pos:         p.Pos,
				PokemonType: p.PokemonType,
			})
		}
		for _, ps := range w.pokestops {
			if !ps.Active {
				continue
			}
		
			snapshot.Pokestops = append(snapshot.Pokestops, EntityDTO{
				ID:                  ps.ID,
				Type:                "pokestop",
				Pos:                 ps.Pos,
				PokestopAvailable:   ps.PokestopAvailable,
			})
		}

		clients := make([]chan Snapshot, 0, len(w.clients))
			for ch := range w.clients {
				clients = append(clients, ch)
			}

		w.mu.Unlock() //desbloqueia a região crítica

		for _, ch := range clients { //atualiza os canais
			select {
			case ch <- snapshot: //ignora canais que não estão prontos
			default:
			}
		}
	}
}


//função para mover pokemon verificando se vai sair de perto do spawn
func movePokemon(e *Pokemon) {
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

	
	if dist > e.MaxRadius { //se está muito longe do spawn, cancela o movimento
		return
	}

	e.Pos.Latitude = newLat
	e.Pos.Longitude = newLon
}

//calcula distância do ponto X até o ponto Y
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

func movePlayerGraphSmooth(e *Player, g *Graph, rng *rand.Rand) {

	// se não tem destino, escolhe um
	if e.TargetNode == "" {
		node := g.Nodes[e.CurrentNode]

		if len(node.Neighbors) == 0 {
			return
		}

		valid := []string{}
		for _, n := range node.Neighbors {
			if n != e.LastNode {
				valid = append(valid, n)
			}
		}

		if len(valid) == 0 {
			valid = node.Neighbors
		}

		e.TargetNode = valid[rng.Intn(len(valid))]
		e.Progress = 0.0
	}

	start := g.Nodes[e.CurrentNode]
	end := g.Nodes[e.TargetNode]

	// distância total
	dist := distanceMeters(start.Lat, start.Lon, end.Lat, end.Lon)

	if dist == 0 {
		return
	}

	// quanto anda por tick
	step := e.Speed / dist
	e.Progress += step

	// clamp
	if e.Progress >= 1.0 {
		// chegou no nó
		e.LastNode = e.CurrentNode
		e.CurrentNode = e.TargetNode
		e.TargetNode = ""
		e.Progress = 0.0

		e.Pos.Latitude = end.Lat
		e.Pos.Longitude = end.Lon
		e.Pos.Timestamp = time.Now()

		e.Path = append(e.Path, e.Pos)
		return
	}

	// interpolação linear
	lat := start.Lat + (end.Lat-start.Lat)*e.Progress
	lon := start.Lon + (end.Lon-start.Lon)*e.Progress

	e.Pos.Latitude = lat
	e.Pos.Longitude = lon
	e.Pos.Timestamp = time.Now()

	e.Path = append(e.Path, e.Pos)
}

// lida com as interações do jogador, tanto com pokémons quanto com pokestops
func (w *World) handleInteractions(rng *rand.Rand) {
	for _, player := range w.players {

		if !player.Active {
			continue
		}

		for _, pokemon := range w.pokemons {

			if !pokemon.Active {
				continue
			}


			dist := distanceMeters(
				player.Pos.Latitude,
				player.Pos.Longitude,
				pokemon.Pos.Latitude,
				pokemon.Pos.Longitude,
			)

			if dist > 40.0 { //se estiver a menos de 40 metros de um pokémon ou pokestop, faz uma ação
				continue
			}

				chance := 0.5// 50% de chance de capturar um pokemon
				if player.CaptureBoost {
					chance = 0.8 //se passou por um pokestop antes, chance aumenta para 80%
					player.CaptureBoost = false
				}

				if rng.Float64() < chance {
					player.Pos.Latitude = pokemon.Pos.Latitude //se capturou, vai até a posição do pokemon
					player.Pos.Longitude = pokemon.Pos.Longitude
					player.CapturedCount++
					pokemon.Active = false //desativa o pokemon

					log.Printf("Player %s capturou %s", player.ID, pokemon.ID)
					break

				}
			
		}

		for _, pokestop := range w.pokestops {

			if !pokestop.PokestopAvailable {
				continue
			}
		
			dist := distanceMeters(
				player.Pos.Latitude,
				player.Pos.Longitude,
				pokestop.Pos.Latitude,
				pokestop.Pos.Longitude,
			)
		
			if dist > 40.0 {
				continue
			}
		
			player.Pos.Latitude = pokestop.Pos.Latitude
			player.Pos.Longitude = pokestop.Pos.Longitude
		
			player.CaptureBoost = true
			pokestop.PokestopAvailable = false
			pokestop.CooldownUntil = time.Now().Add(1 * time.Minute)
		
			player.PokestopCount++
		
			log.Printf("Player %s usou Pokestop %s", player.ID, pokestop.ID)
		
			break
		}

			}
		}
	


// funções matemáticas
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


//atualiza o frontend
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

	ch := make(chan Snapshot, 8)

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
		case data := <-ch://recebe dado
			payload, _ := json.Marshal(data) //convetre pra json
			fmt.Fprintf(wr, "data: %s\n\n", payload)
			flusher.Flush()

		case <-r.Context().Done():
			return
		}
	}
}

//cria as tabelas do banco de dados
func initDB() *sql.DB {
	os.Remove("tracker.db") //deleta o bd antigo

	db, err := sql.Open("sqlite3", "tracker.db")
	if err != nil {
		log.Fatal(err)
	}

	//tabela de posições
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

	//tabela de players
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


//atualiza as tabelas
func (w *World) savePlayers(db *sql.DB) {
	w.mu.Lock()

	players := make([]*Player, 0, len(w.players))
	for _, p := range w.players {
		players = append(players, p)
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

	}

	err = tx.Commit()
	if err != nil {
		log.Println("erro no commit:", err)
	}

	//reseta os vetores para não desperdiçar memória
	w.mu.Lock()
	for _, p := range w.players {
		p.Path = nil
		p.CapturedCount = 0
		p.PokestopCount = 0
	}
	w.mu.Unlock()
}

func LoadGraphFromRaw(path string) *Graph {
	file, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	var raw []RawNode

	if err := json.NewDecoder(file).Decode(&raw); err != nil {
		log.Fatal(err)
	}

	g := &Graph{
		Nodes: make(map[string]*Node),
	}

	// cria nós
	for _, r := range raw {
		if !isInsideCity(r.Lat, r.Lon) {
			continue
		}
		
		if r.StreetCount < 2 {
			continue
		}
		id := fmt.Sprintf("%d", r.ID)

		g.Nodes[id] = &Node{
			ID:  id,
			Lat: r.Lat,
			Lon: r.Lon,
		}
	}
	const maxDist = 200.0 // metros

	for _, n1 := range g.Nodes {
		for _, n2 := range g.Nodes {

			if n1.ID == n2.ID {
				continue
			}

			dist := distanceMeters(n1.Lat, n1.Lon, n2.Lat, n2.Lon)

			if dist < maxDist {
				n1.Neighbors = append(n1.Neighbors, n2.ID)
			}
		}
	}

	return g
}

func main() {
	graph := LoadGraphFromRaw("nodes.json")
	world := NewWorld() //cria entidades e canais
	world.graph = graph

	//spawna entidades
	world.SpawnPokemons(1500)
	world.SpawnPlayersFromGraph(400, graph)
	world.SpawnPokeStops(150)

	//simulação
	go world.StartSimulation()
	db := initDB()
	go func() {
		for {
			time.Sleep(1 * time.Minute) // a cada minuto, salva no bd
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