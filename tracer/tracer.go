package tracer

import (
	"github.com/achilleasa/go-pathtrace/scene"
)

// A unit of work that is processed by a tracer.
type BlockRequest struct {
	// Block start row and height.
	BlockY int
	BlockH int

	// Dimensions of the rendered frame.
	FrameW int
	FrameH int

	// The number of emitted rays per traced pixel.
	SamplesPerPixel int

	// The exposure value controls HDR -> LDR mapping.
	Exposure float32

	// A channel to signal on block completion with the number of completed rows.
	DoneChan chan<- int

	// A channel to signal if an error occurs.
	ErrChan chan<- error
}

type Tracer interface {
	// Get tracer id.
	Id() string

	// Shutdown and cleanup tracer.
	Close()

	// Get the tracers computation speed estimate compared to a
	// baseline (cpu) impelemntation.
	SpeedEstimate() float32

	// Attach tracer to render target and start processing incoming block requests.
	Attach(sc *scene.Scene, renderTarget []float32) error

	// Enqueue blcok request.
	Enqueue(blockReq BlockRequest)
}