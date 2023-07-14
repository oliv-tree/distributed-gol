package main

import (
	"log"
	"net"
	"net/rpc"
	"os"
	"sync"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
)

type Board struct{
	cells [][]uint8
	width int
	height int
}

type Game struct {
	current *Board
	advanced *Board
}

func handleError(message string, err error) {
	if err != nil {
		log.Fatal(message, ": ", err)
	}
}

type SecretWorkerOperation struct {}

// Makes a Board given the width, height
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

// Makes a Game given the width, height and the cells to initialise it with
func createGame(width int, height int, startingBoard [][]uint8) *Game {
	current := &Board{cells: startingBoard,width: width,height: height}
	advanced := createBoard(width, height)
	return &Game{
		current:        current,
		advanced:       advanced,
	}
}

// Get retrieves the value of a cell
func (board *Board) Get(x int, y int) uint8 {
	return board.cells[y][x]
}

// Set sets the value of a cell
func (board *Board) Set(x int, y int, val uint8) {
	board.cells[y][x] = val
}

// Alive checks if a cell is alive, accounting for wrap around if necessary
func (board *Board) Alive(x int, y int, wrap bool) bool {
	if wrap {
		x = (x + board.width) % board.width // need to add the w and h for these as Go modulus doesn't like negatives
		y = (y + board.height) % board.height
	}
	return board.Get(x, y) == 255
}

// AdvanceCell advances the specified cell by one turn
func (game *Game) AdvanceCell(x int, y int) {
	aliveNeighbours := game.current.Neighbours(x, y)
	var newCellValue uint8
	if game.current.Alive(x,y, false) { // if the cell is alive
		if aliveNeighbours < 2 || aliveNeighbours > 3 {
			newCellValue = 0 // dies
		} else {
			newCellValue = 255 // stays the same
		}
	} else { // if the cell is dead
		if aliveNeighbours == 3 {
			newCellValue = 255 // becomes alive
		} else {
			newCellValue = 0 // stays the same
		}
	}
	game.advanced.Set(x, y, newCellValue)
}

// Neighbours checks all cells within 1 cell, then checks if each of these are alive to get the returned neighbour count
func (board *Board) Neighbours(x int, y int) int {
	aliveNeighbours := 0
	for i := -1; i <= 1; i++ {
		for j := -1; j <= 1; j++ {
			if i == 0 && j == 0 { // ensures we aren't counting the cell itself
				continue
			}
			if board.Alive(x+j, y+i, true) { // increase count if this cell is alive
				aliveNeighbours++
			}
		}
	}
	return aliveNeighbours
}

// makeMiniBoard returns only the part of the board we have updated
func (game *Game) makeMiniBoard(startY int, endY int) [][]uint8 {
	var currentMiniBoard [][]uint8
	for i:=startY; i<endY; i++ { // only get relevant part of the board (within specified range of Y)
		currentMiniBoard = append(currentMiniBoard, game.advanced.cells[i])
	}
	return currentMiniBoard
}

func (game *Game) AdvanceMiniSection(startX int, endX int, startY int, endY int) {
	for j:=startY; j<endY; j++ { // advance every cell
		for i:=startX; i<endX; i++ {
			game.AdvanceCell(i, j)
		}
	}
}

func (game *Game) SpawnMiniAdvanceWorker(wg *sync.WaitGroup, startX int, endX int, startY int, endY int) {
	defer wg.Done()
	game.AdvanceMiniSection(startX, endX, startY, endY)
}

// AdvanceSection advances the section given to our workers by one turn and returns it
func (s *SecretWorkerOperation) AdvanceSection(request stubs.WorkerRequest, response *stubs.WorkerResponse) (err error) {
	startX := 0
	endX := request.Width
	startY := request.StartY
	endY := request.EndY
	game := createGame(endX, request.Height, request.CurrentBoard)
	workers := 2
	var wg sync.WaitGroup
	miniWorkerHeight := (endY - startY) / workers // number of rows given to each worker
	for i:=0; i<workers; i++ {
		select {
		case <-closed: // exit if the broker has told us to close
			return
		default:
		}
		miniStartY := startY + (i * miniWorkerHeight)
		var miniEndY int
		if i == workers-1 { // make the last worker take the remaining space
			miniEndY = endY
		} else {
			miniEndY = startY + ((i + 1) * miniWorkerHeight)
		}
		wg.Add(1)
		go game.SpawnMiniAdvanceWorker(&wg, startX, endX, miniStartY, miniEndY)
	}
	wg.Wait() // wait for all sub-workers to be done
	response.AdvancedMiniBoard = game.makeMiniBoard(startY, endY) // return only what we updated
	return
}

func (s *SecretWorkerOperation) CloseWorker(_ stubs.Request, _ *stubs.Response) (err error) {
	close(closed)
	return
}

func checkClosed() {
	select {
	case <-closed:
		time.Sleep(1 * time.Second) // wait in case anything is still being called
		os.Exit(0)
	}
}

var closed = make(chan struct{})
func main(){
	err := rpc.Register(&SecretWorkerOperation{})
	handleError("Register error", err)
	listener, err := net.Listen("tcp",":8031")
	go checkClosed()
	handleError("Listener error", err)

	defer func(listener net.Listener) {
		err := listener.Close()
		handleError("Close listener error", err)
	}(listener)
	rpc.Accept(listener)
}
