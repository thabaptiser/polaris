package opencl

import (
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"os"
	"time"
	"unsafe"

	"github.com/achilleasa/polaris/tracer"
	"github.com/achilleasa/polaris/tracer/opencl/device"
	"github.com/go-gl/gl/v2.1/gl"
)

// Debug flags.
type DebugFlag uint16

const (
	NoDebug                     DebugFlag = 0
	PrimaryRayIntersectionDepth           = 1 << iota
	PrimaryRayIntersectionNormals
	AllEmissiveSamples
	VisibleEmissiveSamples
	OccludedEmissiveSamples
	Throughput
	Accumulator
	FrameBuffer
)

// An alias for functions that can be used as part of the rendering pipeline.
type PipelineStage func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error)

// The list of pluggable of stages that are used to render the scene.
type Pipeline struct {
	// Reset the tracer state. This stage is executed whenever the camera
	// is moved or the sample counter is reset.
	Reset PipelineStage

	// This stage is executed whenever the tracer generates a new set
	// of primary rays. Depending on the samples per pixel this stage
	// may be invoked more than once.
	PrimaryRayGenerator PipelineStage

	// This stage implements an integrator function to trace the primary
	// rays and add their contribution into the accumulation buffer.
	Integrator PipelineStage

	// A set of post-processing stages that are executed prior to
	// rendering the final frame.
	PostProcess []PipelineStage
}

func DefaultPipeline(debugFlags DebugFlag) *Pipeline {
	pipeline := &Pipeline{
		Reset:               ClearAccumulator(),
		PrimaryRayGenerator: PerspectiveCamera(),
		Integrator:          MonteCarloIntegrator(debugFlags),
		PostProcess: []PipelineStage{
			TonemapSimpleReinhard(),
		},
	}

	if debugFlags&FrameBuffer == FrameBuffer {
		pipeline.PostProcess = append(pipeline.PostProcess, SaveFrameBuffer("debug-fb.png"))
	}

	return pipeline
}

// Clear the frame accumulator buffer.
func ClearAccumulator() PipelineStage {
	return func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error) {
		return tr.resources.ClearFrameAccumulator(blockReq)
	}
}

// Use a perspective camera for the primary ray generation stage.
func PerspectiveCamera() PipelineStage {
	return func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error) {
		return tr.resources.GeneratePrimaryRays(blockReq, tr.cameraPosition, tr.cameraFrustrum)
	}
}

// Apply simple Reinhard tone-mapping.
func TonemapSimpleReinhard() PipelineStage {
	return func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error) {
		return tr.resources.TonemapSimpleReinhard(blockReq)
	}
}

// Use a montecarlo pathtracer implementation.
func MonteCarloIntegrator(debugFlags DebugFlag) PipelineStage {
	return func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error) {
		var err error

		start := time.Now()
		numPixels := int(blockReq.FrameW * blockReq.BlockH)
		numEmissives := uint32(len(tr.sceneData.EmissivePrimitives))

		var activeRayBuf uint32 = 0

		// Intersect primary rays outside of the loop
		// Use packet query intersector for GPUs as opencl forces CPU
		// to use a local workgroup size equal to 1
		if tr.device.Type == device.GpuDevice {
			_, err = tr.resources.RayPacketIntersectionQuery(activeRayBuf, numPixels)
		} else {
			_, err = tr.resources.RayIntersectionQuery(activeRayBuf, numPixels)
		}
		if err != nil {
			return time.Since(start), err
		}

		if debugFlags&PrimaryRayIntersectionDepth == PrimaryRayIntersectionDepth {
			_, err = tr.resources.DebugRayIntersectionDepth(blockReq, activeRayBuf)
			err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, "debug-primary-intersection-depth.png")
			if err != nil {
				return time.Since(start), err
			}
		}
		if debugFlags&PrimaryRayIntersectionNormals == PrimaryRayIntersectionNormals {
			_, err = tr.resources.DebugRayIntersectionNormals(blockReq, activeRayBuf)
			err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, "debug-primary-intersection-normals.png")
			if err != nil {
				return time.Since(start), err
			}
		}

		var bounce uint32
		for bounce = 0; bounce < blockReq.NumBounces; bounce++ {
			// Shade misses
			if tr.sceneData.SceneDiffuseMatIndex != -1 {
				if bounce == 0 {
					_, err = tr.resources.ShadePrimaryRayMisses(uint32(tr.sceneData.SceneDiffuseMatIndex), activeRayBuf, numPixels)
				} else {
					_, err = tr.resources.ShadeIndirectRayMisses(uint32(tr.sceneData.SceneDiffuseMatIndex), activeRayBuf, numPixels)
				}
				if err != nil {
					return time.Since(start), err
				}
			}

			// Shade hits
			_, err = tr.resources.ShadeHits(bounce, blockReq.MinBouncesForRR, rand.Uint32(), numEmissives, activeRayBuf, numPixels)
			if err != nil {
				return time.Since(start), err
			}

			if debugFlags&Throughput == Throughput {
				_, err = tr.resources.DebugThroughput(blockReq)
				err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, fmt.Sprintf("debug-throughput-%03d.png", bounce))
				if err != nil {
					return time.Since(start), err
				}
			}

			// Process intersections for occlusion rays and accumulate emissive samples for non occluded paths
			_, err := tr.resources.RayIntersectionTest(2, numPixels)
			if err != nil {
				return time.Since(start), err
			}

			_, err = tr.resources.AccumulateEmissiveSamples(2, numPixels)
			if err != nil {
				return time.Since(start), err
			}

			if debugFlags&AllEmissiveSamples == AllEmissiveSamples {
				_, err = tr.resources.DebugEmissiveSamples(blockReq, 0, 0)
				err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, fmt.Sprintf("debug-emissive-all-%03d.png", bounce))
				if err != nil {
					return time.Since(start), err
				}
			}

			if debugFlags&VisibleEmissiveSamples == VisibleEmissiveSamples {
				_, err = tr.resources.DebugEmissiveSamples(blockReq, 1, 0)
				err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, fmt.Sprintf("debug-emissive-vis-%03d.png", bounce))
				if err != nil {
					return time.Since(start), err
				}
			}

			if debugFlags&OccludedEmissiveSamples == OccludedEmissiveSamples {
				_, err = tr.resources.DebugEmissiveSamples(blockReq, 0, 1)
				err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, fmt.Sprintf("debug-emissive-occ-%03d.png", bounce))
				if err != nil {
					return time.Since(start), err
				}
			}

			if debugFlags&Accumulator == Accumulator {
				_, err = tr.resources.DebugAccumulator(blockReq)
				err = dumpDebugBuffer(err, tr.resources, blockReq.FrameW, blockReq.FrameH, fmt.Sprintf("debug-accumulator-%03d.png", bounce))
				if err != nil {
					return time.Since(start), err
				}
			}

			// Process intersections for indirect rays
			if bounce+1 < blockReq.NumBounces {
				activeRayBuf = 1 - activeRayBuf
				_, err = tr.resources.RayIntersectionQuery(activeRayBuf, numPixels)
				if err != nil {
					return time.Since(start), err
				}
			}
		}
		return time.Since(start), nil
	}
}

// Save a copy of the RGBA framebuffer.
func SaveFrameBuffer(imgFile string) PipelineStage {
	return func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error) {
		start := time.Now()

		f, err := os.Create(imgFile)
		if err != nil {
			return 0, err
		}
		defer f.Close()

		im := image.NewRGBA(image.Rect(0, 0, int(blockReq.FrameW), int(blockReq.FrameH)))
		err = tr.resources.buffers.FrameBuffer.ReadData(0, 0, tr.resources.buffers.FrameBuffer.Size(), im.Pix)
		if err != nil {
			return 0, err
		}

		return time.Since(start), png.Encode(f, im)
	}
}

// Copy RGBA screen buffer to opengl texture. This function assumes that
// the caller has enabled the appropriate 2D texture target.
func CopyFrameBufferToOpenGLTexture() PipelineStage {
	var fbBuf []byte
	return func(tr *Tracer, blockReq *tracer.BlockRequest) (time.Duration, error) {
		start := time.Now()

		fbSizeInBytes := tr.resources.buffers.FrameBuffer.Size()
		if fbBuf == nil || len(fbBuf) != fbSizeInBytes {
			fbBuf = make([]byte, fbSizeInBytes)
		}

		err := tr.resources.buffers.FrameBuffer.ReadData(0, 0, fbSizeInBytes, fbBuf)
		if err != nil {
			return 0, err
		}

		gl.TexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, int32(blockReq.FrameW), int32(blockReq.FrameH), gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&fbBuf[0]))
		return time.Since(start), nil
	}
}

// Dump debug buffer to png file.
func dumpDebugBuffer(debugKernelError error, dr *deviceResources, frameW, frameH uint32, imgFile string) error {
	if debugKernelError != nil {
		return debugKernelError
	}
	f, err := os.Create(imgFile)
	if err != nil {
		return err
	}
	defer f.Close()

	im := image.NewRGBA(image.Rect(0, 0, int(frameW), int(frameH)))
	err = dr.buffers.DebugOutput.ReadData(0, 0, dr.buffers.DebugOutput.Size(), im.Pix)
	if err != nil {
		return err
	}

	return png.Encode(f, im)
}

func readCounter(dr *deviceResources, counterIndex uint32) uint32 {
	out := make([]uint32, 1)
	dr.buffers.RayCounters[counterIndex].ReadData(0, 0, 4, out)
	return out[0]
}
