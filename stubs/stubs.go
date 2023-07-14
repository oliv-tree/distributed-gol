package stubs

import (
	"uk.ac.bris.cs/gameoflife/util"
)

// Distributor calls broker
var StartGameHandler = "SecretBrokerOperation.StartGame"
var AliveCellCountHandler = "SecretBrokerOperation.AliveCellCount"
var CurrentBoardHandler = "SecretBrokerOperation.CurrentBoard"
var CloseBrokerHandler = "SecretBrokerOperation.CloseBroker"
var PauseBrokerHandler = "SecretBrokerOperation.PauseBroker"
var ControllerClosedHandler = "SecretBrokerOperation.ControllerClosed"

// Broker calls worker
var AdvanceSection = "SecretWorkerOperation.AdvanceSection"
var CloseWorkerHandler = "SecretWorkerOperation.CloseWorker"

type Response struct {
	FinishedBoard [][]uint8
	CompletedTurns int
	AliveCells []util.Cell
}

type Request struct {
	StartingBoard [][]uint8
	Height int
	Width int
	Turns int
}

type WorkerResponse struct {
	AdvancedMiniBoard [][]uint8
}

type WorkerRequest struct {
	StartY int
	EndY int
	CurrentBoard [][]uint8
	Width int
	Height int
}
