package gol

import (
	"fmt"
	"log"
	"net/rpc"
	"os"
	"strconv"
	"time"
	"uk.ac.bris.cs/gameoflife/stubs"
)

type distributorChannels struct {
	events     chan<- Event
	ioCommand  chan<- ioCommand
	ioIdle     <-chan bool
	ioFilename chan<- string
	ioOutput   chan<- uint8
	ioInput    <-chan uint8
	keys <-chan rune
}

func handleError(message string, err error) {
	if err != nil {
		log.Fatal(message, ": ", err)
	}
}

// Loads board from input
func createInputBoard(height int, width int, c distributorChannels) [][]uint8 {
	cells := make([][]uint8, height)
	for x := range cells {
		cells[x] = make([]uint8, width)
	}
	for j := 0; j < height; j++ {
		for i := 0; i < width; i++ {
			cells[j][i] = <-c.ioInput
		}
	}
	return cells
}

// MonitorKeyPresses follows the rules when certain keys are pressed
func MonitorKeyPresses(p Params, c distributorChannels, broker *rpc.Client, gameOver chan bool, pauseTicker chan bool) {
	gamePaused := false
	for {
		key := <-c.keys
		switch key {
		case 's': // retrieve current board state and write it as image
			request := new(stubs.Request)
			response := new(stubs.Response)
			err := broker.Call(stubs.CurrentBoardHandler, request, &response)
			handleError("Call broker error", err)
			WriteImage(p, c, response.FinishedBoard, response.CompletedTurns)
		case 'q': // close controller
			err := broker.Call(stubs.ControllerClosedHandler, new(stubs.Request), new(stubs.Response))
			handleError("Call broker error", err)
			err = broker.Close()
			handleError("Close broker error", err)
			os.Exit(0)
		case 'k': // kill controller, broker and workers
			gameOver <- true
			request := new(stubs.Request)
			response := new(stubs.Response)
			err := broker.Call(stubs.CurrentBoardHandler, request, &response) // get current board state
			handleError("Call broker error", err)
			WriteImage(p, c, response.FinishedBoard, response.CompletedTurns) // write board as image
			err = broker.Call(stubs.CloseBrokerHandler, request, &response) // close broker which closes workers
			handleError("Call broker error", err)
			err = broker.Close()
			handleError("Close broker error", err)
			os.Exit(0)
		case 'p': // pause processing
			request := new(stubs.Request)
			response := new(stubs.Response)
			err := broker.Call(stubs.PauseBrokerHandler, request, &response)
			handleError("Call broker error", err)
			if gamePaused { // game was paused
				fmt.Println("Continuing")
				gamePaused = false
			} else { // game un-paused
				fmt.Println("Paused after turn: ", response.CompletedTurns + 1)
				gamePaused = true
			}
			pauseTicker <- gamePaused // tell cell count ticker to continue/stop based on paused state
		}
	}
}

// MonitorAliveCellCount gets the number of alive cells every 2sec from the broker, and submits the event
func MonitorAliveCellCount(broker *rpc.Client, c distributorChannels, gameOver chan bool, pauseTicker chan bool) {
	response := new(stubs.Response)
	request := new(stubs.Request)
	ticker := time.NewTicker(2 * time.Second) // every 2 seconds
	for {
		select {
		case <-gameOver: // check if process has been killed by (pressing k)
			return
		case <-pauseTicker: // check if process paused (by pressing p)
			<-pauseTicker
		case <-ticker.C: // +2 seconds has passed
			err := broker.Call(stubs.AliveCellCountHandler, request, &response)
			handleError("Call broker error", err)
			// get cell count from broker
			c.events <- AliveCellsCount{response.CompletedTurns, len(response.AliveCells)}
		default:

		}
	}
}

// WriteImage outputs the final state of the board as a PGM image
func WriteImage(p Params, c distributorChannels, finishedBoard [][]uint8, completedTurns int) {
	c.ioCommand <- ioOutput
	filename := strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(p.ImageHeight) + "x" + strconv.Itoa(completedTurns)
	c.ioFilename <- filename

	for j := 0; j < p.ImageHeight; j++ { // loop through all the cells
		for i := 0; i < p.ImageWidth; i++ {
			c.ioOutput <- finishedBoard[j][i]
		}
	}
	fmt.Println("Wrote image")
	c.events <- ImageOutputComplete{completedTurns, filename}
}

// distributor initialises the game and the connections required, also monitors alive cell count and key presses
func distributor(p Params, c distributorChannels) {
	// make the filename and pass it through channel
	var filename string
	filename = strconv.Itoa(p.ImageWidth) + "x" + strconv.Itoa(p.ImageHeight)
	c.ioCommand <- ioInput   // start reading the image
	c.ioFilename <- filename // pass the filename of the image

	inputBoard := createInputBoard(p.ImageHeight, p.ImageWidth, c) // create cells from input

	broker, err := rpc.Dial("tcp","127.0.0.1:8030") // connect to our broker
	handleError("Dial broker error", err)
	fmt.Println("Connection done")
	defer func(broker *rpc.Client) {
		err := broker.Close()
		handleError("Close broker error", err)
	}(broker)

	request := stubs.Request{StartingBoard: inputBoard, Height: p.ImageHeight, Width: p.ImageWidth, Turns: p.Turns}
	response := new(stubs.Response)

	gameOver := make(chan bool, 1)
	pauseTicker := make(chan bool)
	go MonitorKeyPresses(p, c, broker, gameOver, pauseTicker) // monitor which keys are pressed in SDL window
	go MonitorAliveCellCount(broker, c, gameOver, pauseTicker) // monitor and retrieve alive cell count every 2s
	err = broker.Call(stubs.StartGameHandler, request, &response) // tell the broker to begin processing
	handleError("Call broker error", err)
	gameOver <- true // broadcasts to monitor cell count goroutine that the game processing is finished

	c.events <- FinalTurnComplete{response.CompletedTurns,response.AliveCells}

	WriteImage(p,c,response.FinishedBoard,response.CompletedTurns)
	// Make sure that the Io has finished any output before exiting.
	c.ioCommand <- ioCheckIdle
	<-c.ioIdle

	c.events <- StateChange{response.CompletedTurns, Quitting}

	// Close the channel to stop the SDL goroutine gracefully. Removing may cause deadlock.
	close(c.events)
}
