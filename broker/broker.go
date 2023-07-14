package main

import (
	"log"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
	"uk.ac.bris.cs/gameoflife/util"
)

type Board struct{
	cells [][]uint8
	width int
	height int
}

type Game struct {
	current *Board
	advanced *Board
	completedTurns int
	mutex sync.Mutex
	paused bool
}

type SecretBrokerOperation struct {}

func handleError(message string, err error) {
	if err != nil {
		log.Fatal(message, ": ", err)
	}
}

func checkClosed() {
	select {
	case <-closed:
		time.Sleep(1 * time.Second) // wait in case anything is still being called
		os.Exit(0)
	}
}

// createBoard creates a board struct given a width and height
// Note we create the columns first, so we need to do cells[y][x]
func createBoard(width int, height int) *Board {
	cells := make([][]uint8, height)
	for x := range cells {
		cells[x] = make([]uint8, width)
	}
	return &Board{
		cells:  cells,
		width:  width,
		height: height,
	}
}

// createGame creates an instance of Game
func createGame(width int, height int, startingBoard [][]uint8) *Game {
	current := &Board{cells: startingBoard,width: width,height: height}
	advanced := createBoard(width, height)
	return &Game{
		current:        current,
		advanced:       advanced,
		completedTurns: 0,
		paused: 		false,
	}
}

// Get retrieves the value of a cell
func (board *Board) Get(x int, y int) uint8 {
	return board.cells[y][x]
}

// Alive checks if a cell is alive, accounting for wrap around if necessary
func (board *Board) Alive(x int, y int, wrap bool) bool {
	if wrap {
		x = (x + board.width) % board.width // need to add the w and h for these as Go modulus doesn't like negatives
		y = (y + board.height) % board.height
	}
	return board.Get(x, y) == 255
}


// AliveCells returns a list of Cells that are alive at the end of the game
func (board *Board) AliveCells() []util.Cell {
	var aliveCells []util.Cell
	for j := 0; j < board.height; j++ {
		for i := 0; i < board.width; i++ {
			if board.Alive(i, j, false) {
				aliveCells = append(aliveCells, util.Cell{X: i, Y: j})
			}
		}
	}
	return aliveCells
}


// Advance splits the board into horizontal slices. Each worker works on one section to advance the whole board one turn
func (game *Game) Advance(workers int, width int, height int, workerClients []*rpc.Client) {
	var doneChannels []chan *rpc.Call // signal through this channel when the worker has finished the job
	var responses []*stubs.WorkerResponse // all the workers' work
	for i := 0; i < workers; i++ {
		startY := i * height / workers
		var endY int
		if i == workers-1 { // make the last worker take the remaining space
			endY = height
		} else {
			endY = (i + 1) * height / workers
		}
		request := stubs.WorkerRequest{StartY: startY, EndY: endY, Width: width, Height: height, CurrentBoard: game.current.cells}
		responses = append(responses, new(stubs.WorkerResponse)) // add response for this worker
		doneChannels = append(doneChannels, make(chan *rpc.Call, 1))
		workerClients[i].Go(stubs.AdvanceSection, request, &responses[i], doneChannels[i])
	}
	// now wait for all the work to be done
	for i:=0; i<workers; i++ {
		<-doneChannels[i]
	}
	game.Reassemble(responses)
}

// Reassemble takes all the slices from workers and reassemble them to update the advanced board
func (game *Game) Reassemble(responses []*stubs.WorkerResponse){
	count := 0
	for _, response := range responses {
		for _, row := range response.AdvancedMiniBoard {
			game.advanced.cells[count] = row // making sure the order of the slices is correct
			count++
		}
	}
}

// ExecuteTurns calls n workers and distributes the processing of the board among them
func (game *Game) ExecuteTurns(turns int){
	//addresses := []string{"18.212.5.104:8030", "54.157.44.67:8030", "3.94.203.220:8030", "54.161.136.245:8030"}
	addresses := []string{"127.0.0.1:8031", "127.0.0.1:8032", "127.0.0.1:8033", "127.0.0.1:8034"}
	var workerClients []*rpc.Client
	for _, address := range addresses { // dial to each worker in our list of addresses
		worker, err := rpc.Dial("tcp", address)
		handleError("Dial worker error", err)
		workerClients = append(workerClients, worker)
	}
	for game.completedTurns < turns {
		select {
		case <-controllerClosed: // controller has closed, so we stop game and wait for a new one
			return
		case <-pauseTurns: // controller has told us to pause
			<-pauseTurns // wait for unpause
		case <-closeWorkers: // controller has told us to close everything
			for _, w := range workerClients { // tell each worker to close
				err := w.Call(stubs.CloseWorkerHandler, new(stubs.Request), new(stubs.Response))
				handleError("Call worker error", err)
				err = w.Close()
				handleError("Close worker error", err)
			}
			close(workersClosed) // signal we are done closing the workers
			return
		default:
		}
		game.mutex.Lock() // lock in case AliveCellCount required whilst swapping the board
		game.Advance(len(addresses), game.current.width, game.current.height, workerClients)
		game.current, game.advanced = game.advanced, game.current
		game.completedTurns++
		game.mutex.Unlock()
	}
}

// StartGame starts initialising game and executing when distributor calls
func (s *SecretBrokerOperation) StartGame(req stubs.Request, res *stubs.Response)(err error){
	startingBoard := req.StartingBoard
	currentGame = createGame(req.Height,req.Width,startingBoard)
	currentGame.ExecuteTurns(req.Turns) // begin game
	res.FinishedBoard = currentGame.current.cells
	res.CompletedTurns = currentGame.completedTurns
	res.AliveCells = currentGame.current.AliveCells()
	return
}

// AliveCellCount return alive Cells to distributor
func (s *SecretBrokerOperation) AliveCellCount(_ stubs.Request, response *stubs.Response)(err error){
	currentGame.mutex.Lock() // lock so turns don't continue whilst counting
	response.FinishedBoard = currentGame.current.cells
	response.CompletedTurns = currentGame.completedTurns
	response.AliveCells = currentGame.current.AliveCells()
	currentGame.mutex.Unlock()
	return
}

// CurrentBoard return current board to distributor
func (s *SecretBrokerOperation) CurrentBoard(_ stubs.Request, response *stubs.Response) (err error) {
	response.FinishedBoard = currentGame.current.cells
	response.CompletedTurns = currentGame.completedTurns
	return
}

// CloseBroker close the broker
func (s *SecretBrokerOperation) CloseBroker(_ stubs.Request, _ *stubs.Response) (err error) {
	close(closeWorkers) // signal we need to close workers
	<-workersClosed // wait until workers have been closed
	close(closed)
	return
}

// PauseBroker pause the broker
func (s *SecretBrokerOperation) PauseBroker(_ stubs.Request, response *stubs.Response) (err error) {
	if currentGame.paused {
		currentGame.paused = false
	} else {
		currentGame.paused = true
	}
	pauseTurns <- currentGame.paused
	response.CompletedTurns = currentGame.completedTurns
	return
}

func (s *SecretBrokerOperation) ControllerClosed(_ stubs.Request, _ *stubs.Response) (err error) {
	controllerClosed <- true
	return
}

var currentGame *Game
var pauseTurns = make(chan bool)
var closeWorkers = make(chan struct{})
var workersClosed = make(chan struct{})
var controllerClosed = make(chan bool)
var closed = make(chan struct{})

func main(){
	err := rpc.Register(&SecretBrokerOperation{})
	handleError("Register error", err)
	listener, err := net.Listen("tcp",":8030")
	go checkClosed()
	handleError("Listener error", err)

	defer func(listener net.Listener) {
		err := listener.Close()
		handleError("Close listener error", err)
	}(listener)
	rpc.Accept(listener)
}