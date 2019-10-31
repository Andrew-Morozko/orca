package jobcontroller

import (
	"context"
	"github.com/Andrew-Morozko/orca/mylog"
	"os"
	"os/signal"
	"sync"
	"time"
)

type ShutdownStatus uint8

const (
	Working ShutdownStatus = iota
	Requested
	Demanded
	Forced
	Done
)

type ShutdownController struct {
	requestDeadline  time.Duration
	requestDeadlineT *time.Timer
	demandDeadline   time.Duration
	demandDeadlineT  *time.Timer

	lock   sync.Mutex
	status ShutdownStatus

	shutdownRequestC chan struct{}

	ShutdownCtx      context.Context
	closeShutdownCtx context.CancelFunc

	CleanupCtx      context.Context
	closeCleanupCtx context.CancelFunc

	shutdownDoneC chan struct{}
	wg            sync.WaitGroup
}
type Option func(*ShutdownController) error

// Turn Request into Demand (force-shutdown connections) after duration
func RequestDeadline(duration time.Duration) Option {
	return func(sc *ShutdownController) error {
		sc.requestDeadline = duration
		return nil
	}
}

// Force demand (demand without cleanup) after duration
func DemandDeadline(duration time.Duration) Option {
	return func(sc *ShutdownController) error {
		sc.demandDeadline = duration
		return nil
	}
}

// Force demand (demand without cleanup) after duration
func InterruptHandler(logger *mylog.Logger) Option {
	return func(sc *ShutdownController) error {
		interruptChan := make(chan os.Signal, 2)
		signal.Notify(interruptChan, os.Interrupt)

		go func() {
			interruptCount := 0
			for {
				select {
				case <-interruptChan:
					switch interruptCount {
					case 0:
						interruptCount++
						sc.Request()
						if logger != nil {
							logger.Log(
								"Shutdown requested, waiting until every connection is closed\n",
								"Press [Ctrl+C] again to shutdown server immediatly",
							)
						}
					case 1:
						interruptCount++
						sc.Demand()
						if logger != nil {
							logger.Log(
								"Shutdown demanded, forcibly shutting down the server\n",
								"Press [Ctrl+C] again to cancel cleanup procedures",
							)
						}
					case 2:
						sc.Force()
						if logger != nil {
							logger.Log(
								"Shutdown forced :-/",
							)
						}
						return
					}
				case <-sc.Done():
					return
				}
			}
		}()

		return nil
	}
}

// should be launched only once (on transition from Working state)
func (sc *ShutdownController) shutdownWaiter() {
	sc.wg.Wait()
	sc.lock.Lock()
	close(sc.shutdownDoneC)
	sc.status = Done
	if sc.demandDeadlineT != nil {
		sc.demandDeadlineT.Stop()
		sc.demandDeadlineT = nil
	}
	if sc.requestDeadlineT != nil {
		sc.requestDeadlineT.Stop()
		sc.requestDeadlineT = nil
	}
	sc.lock.Unlock()
}

func (sc *ShutdownController) ShutdownRequested() <-chan struct{} {
	return sc.shutdownRequestC
}

// Servers should deny new connections
func (sc *ShutdownController) Request() {
	sc.lock.Lock()
	if sc.status < Requested {
		if sc.status == Working {
			go sc.shutdownWaiter()
		}
		close(sc.shutdownRequestC)
		sc.status = Requested
		if sc.requestDeadline != -1 {
			sc.requestDeadlineT = time.AfterFunc(sc.requestDeadline, sc.Demand)
		}
	}
	sc.lock.Unlock()
}

// Servers should terminate connections and cleanup
func (sc *ShutdownController) Demand() {
	sc.lock.Lock()
	if sc.status < Demanded {
		if sc.status == Working {
			go sc.shutdownWaiter()
		}
		sc.closeShutdownCtx()
		sc.status = Demanded
		if sc.demandDeadline != -1 {
			sc.demandDeadlineT = time.AfterFunc(sc.demandDeadline, sc.Force)
		}
		if sc.requestDeadlineT != nil {
			sc.requestDeadlineT.Stop()
			sc.requestDeadlineT = nil
		}
	}
	sc.lock.Unlock()
}

// Extreme measure. Shutdown w/o cleanup
func (sc *ShutdownController) Force() {
	sc.lock.Lock()
	if sc.status < Forced {
		if sc.status == Working {
			go sc.shutdownWaiter()
		}
		sc.closeShutdownCtx()
		sc.status = Forced
		if sc.demandDeadlineT != nil {
			sc.demandDeadlineT.Stop()
			sc.demandDeadlineT = nil
		}
		if sc.requestDeadlineT != nil {
			sc.requestDeadlineT.Stop()
			sc.requestDeadlineT = nil
		}
	}
	sc.lock.Unlock()
}

func (sc *ShutdownController) IsShuttingDown() bool {
	sc.lock.Lock()
	defer sc.lock.Unlock()
	switch sc.status {
	case Working, Done:
		return false
	default:
		return true
	}
}

func (sc *ShutdownController) ShutdownStatus() ShutdownStatus {
	sc.lock.Lock()
	defer sc.lock.Unlock()
	return sc.status
}

func (sc *ShutdownController) Done() <-chan struct{} {
	return sc.shutdownDoneC
}

func New(ctx context.Context, options ...Option) (sc *ShutdownController, err error) {
	sc = &ShutdownController{
		requestDeadline:  -1,
		demandDeadline:   -1,
		shutdownDoneC:    make(chan struct{}),
		shutdownRequestC: make(chan struct{}),
	}

	// Force
	sc.CleanupCtx, sc.closeCleanupCtx = context.WithCancel(ctx)

	// Demand
	sc.ShutdownCtx, sc.closeShutdownCtx = context.WithCancel(sc.CleanupCtx)

	for _, opt := range options {
		err = opt(sc)
		if err != nil {
			return nil, err
		}
	}

	return
}

// One global object to manage the state of the system
type JobController struct {
	Job struct {
		// Add job that would be awaited before server shutdown
		Add func(delta int)
		// Mark a job as done
		Done func()
	}
	shutdownRequestC chan struct{}
	// Current context to use
	context.Context
	// Server shutdown context, Done on demand to shutdown
	ShutdownCtx context.Context
	// Server cleanup context, Done on force
	CleanupCtx context.Context

	//
	IsShuttingDown func() bool
	ShutdownStatus func() ShutdownStatus

	// Current logger to use
	Logger *mylog.Logger
}

func (jc JobController) ShutdownRequested() <-chan struct{} {
	return jc.shutdownRequestC
}

func (sc *ShutdownController) GetJobController(logger *mylog.Logger) JobController {
	jc := JobController{
		shutdownRequestC: sc.shutdownRequestC,

		Context:     sc.ShutdownCtx,
		ShutdownCtx: sc.ShutdownCtx,
		CleanupCtx:  sc.CleanupCtx,

		IsShuttingDown: sc.IsShuttingDown,
		ShutdownStatus: sc.ShutdownStatus,

		Logger: logger,
	}
	jc.Job.Add = sc.wg.Add
	jc.Job.Done = sc.wg.Done
	return jc
}

func (jc JobController) NewCtx(ctx context.Context) JobController {
	jc.Context = ctx
	return jc
}
func (jc JobController) AddLoggerPrefix(prefix string) JobController {
	jc.Logger = jc.Logger.NewWithPrefix(prefix)
	return jc
}
