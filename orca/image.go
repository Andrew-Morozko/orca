package orca

import (
	"fmt"

	"github.com/Andrew-Morozko/orca/jobcontroller"
	"github.com/Andrew-Morozko/orca/orca/mydocker"

	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/pkg/errors"
)

type Image struct {
	Kind            ImageKind
	Name            string
	Port            int
	ConcurrentUsers int
	TotalUsers      int

	DockerID string

	containerConfig  *container.Config
	hostConfig       *container.HostConfig     // if needed
	networkingConfig *network.NetworkingConfig // if needed

	PersistBetweenReconnects bool
	Timeouts                 struct {
		Total    time.Duration
		Inactive time.Duration
	}

	electionRequestC        chan chan contatinerCandidate
	electionStopC           chan struct{}
	getElectionStartSignalC chan chan struct{}
	getCandidatesChan       chan chan contatinerCandidate

	containerLock       sync.Mutex
	containerUsersByUID map[string]*ContainerUser
	// containerUsersByDockerID map[string]*ContainerUser
	// containerName    string
}

func (img *Image) String() string {
	return fmt.Sprintf(`Image{name="%s", id=%s}`, img.Name, strings.Split(img.DockerID, ":")[1][:8])
}

func NewImage(jc jobcontroller.JobController, img *mydocker.Image) (*Image, error) {
	oi := &Image{
		DockerID:                img.ID,
		getCandidatesChan:       make(chan chan contatinerCandidate),
		electionRequestC:        make(chan chan contatinerCandidate),
		electionStopC:           make(chan struct{}),
		getElectionStartSignalC: make(chan chan struct{}),
		containerUsersByUID:     make(map[string]*ContainerUser),
		// containerUsersByDockerID: make(map[string]*ContainerUser),
	}
	var found bool
	oi.Name, found = img.Get("orca.name")
	if !found {
		if len(img.RepoTags) > 0 {
			oi.Name = mydocker.Normalise(strings.Split(img.RepoTags[0], ":")[0])
			found = true
		}
	}
	if !found {
		return nil, errors.New("Can't find the name")
	}

	oi.Kind, found = img.Get("orca.kind")
	if !found {
		return nil, errors.New("Can't find the kind")
	}
	jc.Logger.Logf("Found image %s of kind %s", oi.Name, oi.Kind)

	oi.containerConfig = img.Config

	switch oi.Kind {
	case ImageKindWeb:
		oi.PersistBetweenReconnects = img.GetBoolDefault("orca.container.persistBetweenReconnects", true)
		oi.ConcurrentUsers = img.GetIntDefault("orca.users.concurrent", -1)
		oi.TotalUsers = img.GetIntDefault("orca.users.total", -1)
		// Expose ports
		port, err := nat.NewPort("tcp", img.GetDefault("orca.port", "80"))
		if err != nil {
			return nil, errors.WithMessage(err, "parsing port")
		}
		oi.Port = port.Int()

		// TODO: do we need this?? Maybe just lookup port?
		oi.containerConfig.ExposedPorts = make(map[nat.Port]struct{})
		oi.containerConfig.ExposedPorts[port] = struct{}{}

		// HACK FOR TESTING
		// oi.hostConfig = &container.HostConfig{
		// 	PortBindings: nat.PortMap{
		// 		port: []nat.PortBinding{
		// 			{
		// 				HostIP:   "0.0.0.0",
		// 				HostPort: "8090",
		// 			},
		// 		},
		// 	},
		// }

	case ImageKindTCP:
		return nil, errors.Errorf("image kind \"%s\" is not implemented", oi.Kind)
	case ImageKindSSH:
		oi.PersistBetweenReconnects = img.GetBoolDefault("orca.container.persistBetweenReconnects", false)
		oi.ConcurrentUsers = img.GetIntDefault("orca.users.concurrent", 1)
		oi.TotalUsers = img.GetIntDefault("orca.users.total", 1)

		cm := img.GetDefault("orca.connection.method", "attach")
		switch cm {
		case "attach":
			oi.containerConfig.AttachStdin = true
			oi.containerConfig.AttachStdout = true
			oi.containerConfig.AttachStderr = true
			oi.containerConfig.Tty = img.GetBoolDefault("orca.container.tty", true)
			oi.containerConfig.NetworkDisabled = img.GetBoolDefault("orca.container.networkdisabled", true)
			oi.containerConfig.OpenStdin = true
			oi.containerConfig.StdinOnce = oi.TotalUsers == 1 // TODO: think about this

		case "connect", "exec":
			return nil, errors.Errorf("connection method \"%s\" is not implemented", cm)
		default:
			return nil, errors.Errorf("unknown connection method \"%s\"", cm)
		}
	default:
		return nil, errors.Errorf("unknown image kind \"%s\"", oi.Kind)

	}
	// Common config parsing
	oi.Timeouts.Total = img.GetDurationDefault("orca.timeout.session", 24*time.Hour)
	oi.Timeouts.Inactive = img.GetDurationDefault("orca.timeout.inactive", 15*time.Minute)

	oi.containerConfig.StopSignal = img.GetDefault(
		"orca.container.stopsignal", oi.containerConfig.StopSignal,
	)

	oi.containerConfig.Labels = map[string]string{
		"orca.internal.managed":   "true",
		"orca.internal.imagename": oi.Name,
	}

	go oi.manageImageState(jc)

	return oi, nil
}

func (oi *Image) MarkRemoved() {
	// TODO
}

func (oi *Image) deleteContainerUser(jc jobcontroller.JobController, cu *ContainerUser) {
	oi.containerLock.Lock()
	if oi.containerUsersByUID[cu.user.ID] == cu {
		// trying to delete active ContainerUser, proceed
		delete(oi.containerUsersByUID, cu.user.ID)
		oi.containerLock.Unlock()
		cu.notifyDeleted()
	} else {
		oi.containerLock.Unlock()
	}
}

func (oi *Image) GetContainerUser(jc jobcontroller.JobController, ui *User) (cu *ContainerUser) {
	// Atomic lookup for ContainerUser
	// Returns object for interaction with container from the POV of the user/(handler)
	oi.containerLock.Lock()
	defer oi.containerLock.Unlock()

	cu = oi.containerUsersByUID[ui.ID]
	if cu != nil && cu.IsAlive() {
		jc.Logger.Debug.Log("Reusing existing ContiainerUser")
		return
	}
	newCu := ui.newContainerUser(jc, oi)
	oi.containerUsersByUID[ui.ID] = newCu
	go cu.notifyDeleted()
	return newCu
}

// Channeled createContainer
func (oi *Image) getContainerC(jc jobcontroller.JobController, ui *User) (<-chan *Container, <-chan error) {
	ocC := make(chan *Container)
	errC := make(chan error)
	jc.Job.Add(1)
	go func() {
		defer jc.Job.Done()
		oc, err := oi.getContainer(jc)
		if err != nil {
			errC <- err
			return
		}
		ocC <- oc
	}()
	return ocC, errC
}

func (oi *Image) manageImageState(jc jobcontroller.JobController) {
	nextElectionSignallingC := make(chan struct{})
	var curElectionCandidatesC chan contatinerCandidate
	var getCandidatesChan chan chan contatinerCandidate
	electionRequestChan := oi.electionRequestC
	var voteStop chan struct{}

	for {
		select {
		// give out vote signal chan to anyone who'll ask
		// asker takes one
		case oi.getElectionStartSignalC <- nextElectionSignallingC:
			jc.Logger.Log("Sent the Election Start signal")
		// getContainer requests an election and send us the candidate chanell
		case curElectionCandidatesC = <-electionRequestChan:
			jc.Logger.Log("Got request for elections")
			// block new elections from happening
			electionRequestChan = nil
			// begin sending out the candidate chanell to askers
			getCandidatesChan = oi.getCandidatesChan
			// signal that election has began
			close(nextElectionSignallingC)
			// generate new signal for next election
			nextElectionSignallingC = make(chan struct{})
			// close this elections
			voteStop = oi.electionStopC
		// send out Candidates chan, takers take one and send their candidacy there
		case getCandidatesChan <- curElectionCandidatesC:
			jc.Logger.Log("Sent election candidates chan")

		// stopping current election, allowing next to begin
		case <-voteStop:
			jc.Logger.Log("Vote stopped")
			// block elections from stopping
			voteStop = nil
			// Allow new elections to happen
			electionRequestChan = oi.electionRequestC
			// stop sending election candidate chanel to listeners
			getCandidatesChan = nil
			// reset current candidatesC (just to be nice)
			curElectionCandidatesC = nil
		case <-jc.Done():
			return
		}
	}
}

var electionsLength = 5 * time.Millisecond

// Finds a container with a free spot. Creates new container if no free spots were found in 5ms
func (oi *Image) getContainer(jc jobcontroller.JobController) (oc *Container, err error) {
	jc = jc.AddLoggerPrefix(fmt.Sprintf("Image %s", oi.Name))

	var requestTimer *time.Timer
	var requestTimerC <-chan time.Time

	attemptDelay := 500 * time.Millisecond
	attamptsRemainings := 5

	var candidates []*contatinerCandidate

	var electionDeadline <-chan time.Time

	candidatesC := make(chan contatinerCandidate)
	electionRequestC := oi.electionRequestC
	for {
		select {
		case electionRequestC <- candidatesC:
			jc.Logger.Log("started election, stopping in 5ms")
			electionDeadline = time.After(electionsLength)
			electionRequestC = nil
		case candidate := <-candidatesC:
			// Get all candidates and their capacities
			jc.Logger.Log("got candidate")
			candidates = append(candidates, &candidate)
		case <-electionDeadline:
			jc.Logger.Log("Stopping the elections")
			oi.electionStopC <- struct{}{}
			candidatesC = nil
			// Evaluate candidates
			jc.Logger.Debug.Log("Election candidates: ", len(candidates), " ", candidates)
			if len(candidates) > 0 {
				best := 0
				for n, candidate := range candidates {
					// Scheduler packs users into as little containers as possible
					// ToDo: various sheduler improvements, for example spread users
					// equaly over minimal number of containers
					if candidate.concurrentUsers > candidates[best].concurrentUsers {
						best = n
					}
				}
				oc = candidates[best].container
				jc.Logger.Debug.Log("best: ", candidates[best], " ", oc)
				if len(candidates) > 1 {
					// Reject candidates except for chosen
					candidates[best] = candidates[len(candidates)-1]
					candidates = candidates[:len(candidates)-1]
					jc.Job.Add(1)
					go func() {
						for _, candidate := range candidates {
							select {
							case candidate.container.candidacyResponce <- nil:
							case <-jc.Done():
								break
							}
						}
						jc.Job.Done()
					}()
				}
				return

			} else {
				// no candidates, request container creation
				requestTimer = time.NewTimer(0)
				requestTimerC = requestTimer.C
			}

		case <-requestTimerC:
			attamptsRemainings--
			jc.Logger.Log("Requesting container creation for image ", oi.Name)

			oc, err = oi.launchContainer(jc)
			jc.Logger.Err(err, "container creation error")
			if err != nil {
				if attamptsRemainings > 0 {
					jc.Logger.Warn.Logf("Trying to create container again, %d attempts left", attamptsRemainings)
					requestTimer.Reset(attemptDelay)
					continue
				} else {
					jc.Logger.Warn.Err(err, "Failed to create contaier")
					return nil, errors.WithMessage(err, "retry exceeded")
				}
			}
			return
		case <-jc.Done():
			return nil, jc.Err()
		}
	}
}

func (oi *Image) IsVisibleTo(ui *User) bool {
	return true
}
