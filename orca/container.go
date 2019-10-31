package orca

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Andrew-Morozko/orca/jobcontroller"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
)

type Container struct {
	DockerID string
	Image    *Image
	// ui       *UserIdentity
	URL *url.URL

	// Status string
	// todo lock, so external observer can see
	concurrentUsers int
	totalUsers      int
	reservedUsers   int
	// userActivity    chan *User // + time?
	users             map[string]*User
	userLeft          chan *User
	candidacyResponce chan *User
}

func (oi *Image) launchContainer(jc jobcontroller.JobController) (oc *Container, err error) {
	jc.Job.Add(1)
	defer jc.Job.Done()
	jc.Logger.Log("Creating a container of ", oi.Name)
	// if needs extra non default config - put here
	contConf := oi.containerConfig

	switch oi.Kind {
	case ImageKindWeb:
		// Todo: deep copy lib?
		contConf = &*contConf
		var newEnv = make([]string, len(oi.containerConfig.Env)+1)
		for i, val := range oi.containerConfig.Env {
			newEnv[i] = val
		}
		// TODO: cleanup this mess
		// TODO: do we even really need this?
		// Derive from host or send in X- header?

		newEnv[len(newEnv)-1] = fmt.Sprintf(
			"ORCA_INTERNAL_CONTAINER_URL="+os.Getenv("ORCA_HTTP_CONTAINER_URL_FORMAT"),
			strings.ToLower(oi.Name),
		)
		contConf.Env = newEnv
	}
	res, err := Docker.ContainerCreate(jc, contConf, oi.hostConfig, oi.networkingConfig, "")
	if err != nil {
		return nil, err
	}
	dockerId := res.ID
	defer func() {
		if err != nil {
			err2 := Docker.ContainerRemove(jc.CleanupCtx, dockerId, types.ContainerRemoveOptions{Force: true})
			jc.Logger.Err(err2, "error while trying to remove a container while exiting out of NewContainer with error")
		}
	}()

	if len(res.Warnings) == 0 {
		jc.Logger.Logf("Container of %s created", oi.Name)
	} else {
		jc.Logger.Warn.Logf("Container of %s created. Warnings:", oi.Name)
		for warn := range res.Warnings {
			jc.Logger.Warn.Log(warn)
		}
	}

	err = Docker.ContainerStart(jc, res.ID, types.ContainerStartOptions{})
	if err != nil {
		return nil, err
	}

	oc = &Container{
		DockerID: dockerId,
		Image:    oi,

		users:             make(map[string]*User),
		userLeft:          make(chan *User),
		candidacyResponce: make(chan *User),
	}
	jc = jc.AddLoggerPrefix(oc.String())

	// Post-config
	switch oi.Kind {
	case ImageKindWeb:
		// inspect, get ip of the container and the port
		res, err := Docker.ContainerInspect(jc, oc.DockerID)
		if err != nil {
			return nil, err
		}
		oc.URL = &url.URL{
			Scheme: "http", // todo: configuarable?
			Host:   fmt.Sprintf("%s:%d", res.NetworkSettings.IPAddress, oc.Image.Port),
			Path:   "/",
			// HACK FOR TESTING
			// Host:   fmt.Sprintf("%s:%d", "<tgt-ip>", 8090),
		}
	}

	// assume that whatever requested our creation reserved us
	// (as if we sent candidacy)
	oc.reservedUsers = 1

	jc.Job.Add(1)
	go oc.manageContainerState(jc)

	return oc, nil
}
func (oc *Container) String() string {
	return fmt.Sprintf(`Container{DockerID=%s}`, oc.DockerID[:8])
}

type contatinerCandidate struct {
	container       *Container
	concurrentUsers int
	totalUsers      int
	// other info to allow user sheduler to make a decidion
}

func (oc *Container) manageContainerState(jc jobcontroller.JobController) {
	// Manages lifecycle of the container
	defer jc.Job.Done()

	defer func() {
		jc.Logger.Debug.Log("lifecycle is over, removing")
		// ContainerUser listens to this and will be notified
		err := Docker.ContainerRemove(jc.CleanupCtx, oc.DockerID,
			types.ContainerRemoveOptions{Force: true})
		jc.Logger.Err(err, "Can't remove")
	}()

	jc.Logger.Debug.Log("Entering lifecycle mangagenet")

	deletionTime := 30 * time.Second // w/o users
	var deletionTimer *time.Timer
	var deletionTimerC <-chan time.Time
	isEndOfLife := false

	candidate := contatinerCandidate{
		container: oc,
	}

	electionsStartSignalC := oc.Image.getElectionStartSignalC
	var electionStartSignal chan struct{}
	var candidatesChannel chan contatinerCandidate
	var getCandidatesChan chan chan contatinerCandidate

	for {
		isEndOfLife = isEndOfLife || oc.totalUsers == oc.Image.TotalUsers
		if oc.concurrentUsers == 0 && oc.reservedUsers == 0 {
			if isEndOfLife || jc.IsShuttingDown() {
				return
			} else {
				if deletionTimer == nil {
					deletionTimer = time.NewTimer(deletionTime)
					deletionTimerC = deletionTimer.C
				}
			}
		} else {
			if oc.concurrentUsers != 0 {
				if deletionTimer != nil {
					if !deletionTimer.Stop() {
						<-deletionTimer.C
					}
					deletionTimer = nil
					deletionTimerC = nil
				}
			}
		}

		jc.Logger.Debug.Logf("oc.concurrentUsers=%d oc.reservedUsers=%d oc.totalUsers=%d isEndOfLife=%v",
			oc.concurrentUsers, oc.reservedUsers, oc.totalUsers, isEndOfLife)

		select {
		case electionStartSignal = <-electionsStartSignalC:
			jc.Logger.Log("got electionStartSignal")
			electionsStartSignalC = nil
		case <-electionStartSignal:
			jc.Logger.Log("electionStartSignal activated")
			// elections started
			electionStartSignal = nil
			// start looking for next signal
			electionsStartSignalC = oc.Image.getElectionStartSignalC
			// if ready to take part in this elections - go ahaed
			if !isEndOfLife && oc.reservedUsers+oc.concurrentUsers != oc.Image.ConcurrentUsers {
				jc.Logger.Log("participating in elections")
				// start looking for candidates chanel
				getCandidatesChan = oc.Image.getCandidatesChan
			}
		case candidatesChannel = <-getCandidatesChan:
			jc.Logger.Log("got candidatesChannel")
			getCandidatesChan = nil
		case candidatesChannel <- candidate:
			jc.Logger.Log("sent candidate")
			// sent our candidature once, that's enough
			candidatesChannel = nil
			oc.reservedUsers++

		case ui := <-oc.candidacyResponce:
			oc.reservedUsers--
			if ui != nil {
				// We were accepted, bind the user to us
				oc.concurrentUsers++
				oc.totalUsers++
				candidate.concurrentUsers = oc.concurrentUsers
				candidate.totalUsers = oc.totalUsers

				// now we have a ui... Just adding it to list for future command panel purpopses
				oc.users[ui.ID] = ui
			}

		case ui := <-oc.userLeft:
			jc.Logger.Debug.Log("User has left")
			oc.concurrentUsers--
			delete(oc.users, ui.ID)

		case <-jc.Done():
			jc.Logger.Debug.Log("Context done")
			return

		case <-deletionTimerC:
			jc.Logger.Debug.Log("Deletion timer kicked off")
			isEndOfLife = true
		}
	}
}

func (oc *Container) GetStream(ctx context.Context) (types.HijackedResponse, error) {
	stream, err := Docker.ContainerAttach(ctx, oc.DockerID, types.ContainerAttachOptions{
		Stream:     true,
		Stdin:      true,
		Stdout:     true,
		Stderr:     true,
		DetachKeys: "",
	})

	return stream, err
}

func (oc *Container) ResizeTTY(ctx context.Context, height, width int) error {
	return Docker.ContainerResize(ctx, oc.DockerID, types.ResizeOptions{
		Height: uint(height),
		Width:  uint(width),
	})
}

func (oc *Container) WaitForShutdown(ctx context.Context) (<-chan container.ContainerWaitOKBody, <-chan error) {
	return Docker.ContainerWait(ctx, oc.DockerID, container.WaitConditionNotRunning)

}
