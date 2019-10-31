package orca

import (
	"github.com/Andrew-Morozko/orca/jobcontroller"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/pkg/errors"
)

// Image.ContainerUsers[UserId.ID] ->ContainerUser (new or cached) (atomically)
type ContainerUser struct {
	user  *User
	image *Image
	// left nil on new
	container *Container

	// handler can listen to this chan
	// ContainerLeft chan time.Time
	// container listens to this
	// Inactivity timeouts, connections breaking, etc
	// UserLeft chan time.Time
	// false or closed - not usable
	// if usable - give this object some time to exist and not shut down the internal loop
	status ContainerStatus

	connectionCount int

	noMoreConnectionsNotification chan struct{}
	activityNotification          chan struct{}
	connectionDroppedNotification chan struct{}

	statusC            chan ContainerStatus
	containerC         chan *Container
	containerAliveC    chan struct{}
	containerShutdownC chan ContainerStatus
}

type ContainerState uint8

const (
	ContainerStateDead ContainerState = iota // null value
	ContainerStateStarting
	ContainerStateWorking
	ContainerStateStartErr
	ContainerStateShutdown
	ContainerStateShutdownInactivity // user was inactive too long
	ContainerStateShutdownSessionLen // user was active too long
	ContainerStateShutdownWithErr
	ContainerStateShutdownWithErrMsg
)

func (cs ContainerState) String() string {
	switch cs {
	case ContainerStateDead:
		return "ContainerStateDead"
	case ContainerStateStarting:
		return "ContainerStateStarting"
	case ContainerStateWorking:
		return "ContainerStateWorking"
	case ContainerStateStartErr:
		return "ContainerStateStartErr"
	case ContainerStateShutdown:
		return "ContainerStateShutdown"
	case ContainerStateShutdownInactivity:
		return "ContainerStateShutdownInactivity"
	case ContainerStateShutdownSessionLen:
		return "ContainerStateShutdownSessionLen"
	case ContainerStateShutdownWithErr:
		return "ContainerStateShutdownWithErr"
	case ContainerStateShutdownWithErrMsg:
		return "ContainerStateShutdownWithErrMsg"
	default:
		return "UnknownContainerState"
	}
}

type ContainerStatus struct {
	ContainerState ContainerState
	Err            error
	Status         int64
}

func (cs ContainerStatus) String() string {
	str := cs.ContainerState.String()
	if cs.Err != nil {
		str += " Err: " + cs.Err.Error()
	}
	if cs.ContainerState == ContainerStateShutdown {
		str += fmt.Sprintf(" Exit Code: %d", cs.Status)
	}
	return str
}

var InactivityTimeoutErr = errors.New("Inactivity Timeout Expired")
var SessionTimeoutErr = errors.New("Total Timeout Expired")

func (cu *ContainerUser) String() string {
	return fmt.Sprintf("ContainerUser{container=%s, user=%s, image=%s}", cu.container, cu.user, cu.image)
}
func (cu *ContainerUser) manageContainerUserState(jc jobcontroller.JobController) {

	defer jc.Job.Done()

	defer func() {
		close(cu.statusC)
		close(cu.containerC)
		close(cu.containerAliveC)
		close(cu.containerShutdownC)
		close(cu.noMoreConnectionsNotification)
		close(cu.activityNotification)
		close(cu.connectionDroppedNotification)
	}()

	var containerDest chan *Container
	var containerShutdownDest chan ContainerStatus
	var containerAliveDest chan struct{}

	var execResultSourceC <-chan container.ContainerWaitOKBody
	var execErrorSourceC <-chan error

	noMoreConnections := !cu.image.PersistBetweenReconnects

	lastState := ContainerStateDead
	containerSourceC, containerSourceErrC := cu.image.getContainerC(jc, cu.user)
	cu.status.ContainerState = ContainerStateStarting

	sessionTimer := time.NewTimer(cu.image.Timeouts.Total)
	inactiveTimer := time.NewTimer(cu.image.Timeouts.Inactive)

	for {
		if lastState != cu.status.ContainerState {
			jc.Logger.Debug.Log("New status: ", cu.status.ContainerState)
			if lastState == ContainerStateStarting {
				// container has started, those channels are useless now
				containerSourceC = nil
				containerSourceErrC = nil
			}

			switch cu.status.ContainerState {
			case ContainerStateStarting, ContainerStateWorking:
				containerAliveDest = cu.containerAliveC
				containerShutdownDest = nil
			default:
				jc.Logger.Debug.Log("Preparing to shutdown")
				containerAliveDest = nil
				containerShutdownDest = cu.containerShutdownC

				// Prob. should execute only once, but deleteContainerUser is safe for multiple executions
				go cu.image.deleteContainerUser(jc, cu)
			}

			// send only working containers
			if cu.status.ContainerState == ContainerStateWorking {
				containerDest = cu.containerC
				execResultSourceC, execErrorSourceC = cu.container.WaitForShutdown(jc)
				// notify container about new user
				select {
				case cu.container.candidacyResponce <- cu.user:
				case <-jc.Done():
				}
				defer func() {
					select {
					case cu.container.userLeft <- cu.user:
					case <-jc.Done():
						return
					}
				}()

			} else {
				containerDest = nil
				execResultSourceC, execErrorSourceC = nil, nil
			}
			lastState = cu.status.ContainerState
		}

		select {

		// Handle data output
		case cu.statusC <- cu.status:
		case containerDest <- cu.container:
			cu.connectionCount++
		case cu.connectionDroppedNotification <- struct{}{}:
			cu.connectionCount--

			if cu.connectionCount == 0 {
				if noMoreConnections {
					return
				}
			}

		case containerAliveDest <- struct{}{}:
		case containerShutdownDest <- cu.status:
			jc.Logger.Debug.Log("containerShutdownDest <- cu.status:")
		// handle container creation
		case cu.container = <-containerSourceC:
			cu.status.ContainerState = ContainerStateWorking

		// handle errors
		case cu.status.Err = <-containerSourceErrC:
			cu.status.ContainerState = ContainerStateStartErr
		case cu.status.Err = <-execErrorSourceC:
			cu.status.ContainerState = ContainerStateShutdownWithErr

		// handle timeouts
		case <-sessionTimer.C:
			cu.status.ContainerState = ContainerStateShutdownSessionLen
			cu.status.Err = SessionTimeoutErr
		case <-inactiveTimer.C:
			cu.status.ContainerState = ContainerStateShutdownInactivity
			cu.status.Err = InactivityTimeoutErr

		// handle container exit
		case st := <-execResultSourceC:
			cu.status.Status = st.StatusCode
			if st.Error != nil && st.Error.Message != "" {
				cu.status.ContainerState = ContainerStateShutdownWithErrMsg
				cu.status.Err = errors.New(st.Error.Message)
			} else {
				cu.status.ContainerState = ContainerStateShutdown
			}

		case cu.activityNotification <- struct{}{}:
			if !inactiveTimer.Stop() {
				<-inactiveTimer.C
			}
			jc.Logger.Debug.Log("Reset timeout for ", cu)
			inactiveTimer.Reset(cu.image.Timeouts.Inactive)

		case cu.noMoreConnectionsNotification <- struct{}{}:
			// no new connections, guaranteed.
			noMoreConnections = true
			if cu.connectionCount == 0 {
				return
			}
		case <-jc.Done():
			return
		}
	}
}

// Callback to mark user being active on the container
func (cu *ContainerUser) Activity() {
	if cu == nil {
		return
	}
	// If containeruser down - nonblocking, since the chanel is closed
	<-cu.activityNotification
}

// Callback to mark user being active on the container
func (cu *ContainerUser) ActivityChan() <-chan struct{} {
	if cu == nil {
		return nil
	}
	return cu.activityNotification
}

// Strictly once after GetContainer
func (cu *ContainerUser) NotifyConnectionClosed() {
	if cu == nil {
		return
	}
	<-cu.connectionDroppedNotification

}

// safe for nil receiver
// after this called - guaranteed that this object will not be
// given out in new connections
func (cu *ContainerUser) notifyDeleted() {
	if cu == nil {
		return
	}
	<-cu.noMoreConnectionsNotification
}

func (cu *ContainerUser) IsAlive() (isAlive bool) {
	isAlive = false
	select {
	case _, isAlive = <-cu.containerAliveC:
	case <-cu.containerShutdownC:
	}
	return
}

func (cu *ContainerUser) ShutdownDone() <-chan ContainerStatus {
	return cu.containerShutdownC
}

// returns running container, blocks while container starting
func (cu *ContainerUser) GetContainer() (oc *Container, status ContainerStatus) {
	select {
	case oc = <-cu.containerC:
		status = <-cu.statusC
		if status.ContainerState != ContainerStateWorking {
			// concurrency is hard lol
			cu.NotifyConnectionClosed()
			return nil, status
		} else {
			return oc, status
		}
	case status = <-cu.containerShutdownC:
		return nil, status
	}

}
