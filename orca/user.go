package orca

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Andrew-Morozko/orca/jobcontroller"

	"github.com/pkg/errors"
)

type User struct {
	// login by default
	ID   string
	lock sync.Mutex
	// publicKeysLoaded bool
	// publicKeys       []string
	taskToken string
}

func (ui *User) String() string {
	return fmt.Sprintf(`User{ID="%s"}`, ui.ID)
}
func (ui *User) newContainerUser(jc jobcontroller.JobController, image *Image) (cu *ContainerUser) {
	cu = &ContainerUser{
		user:               ui,
		image:              image,
		statusC:            make(chan ContainerStatus),
		containerC:         make(chan *Container),
		containerAliveC:    make(chan struct{}),
		containerShutdownC: make(chan ContainerStatus),

		noMoreConnectionsNotification: make(chan struct{}),
		activityNotification:          make(chan struct{}, 1),
		connectionDroppedNotification: make(chan struct{}),
	}
	// creation requested => start the process of creating ?
	jc = jc.AddLoggerPrefix("ContainerUser")
	jc.Logger.Debug.Log("Created new ContainerUser")
	jc.Job.Add(1)
	go cu.manageContainerUserState(jc)

	return
}

type UserList struct {
	lock            sync.Mutex
	users           map[string]*User
	usersByWebtoken map[string]*User
}

func NewUserList() *UserList {
	return &UserList{
		users:           make(map[string]*User),
		usersByWebtoken: make(map[string]*User),
	}
}

func (ul *UserList) GetUser(uid string) (ui *User) {
	ul.lock.Lock()
	defer ul.lock.Unlock()
	ui = ul.users[uid]

	return
}

func (ul *UserList) GetUserByPubKey(login, publicKey string) (ui *User, err error) {
	panic("not implemented")
}
func (ul *UserList) GetUserByLoginPassword(login, password string) (ui *User, err error) {
	// ldap auth
	panic("not implemented")
}

// todo: task token is looked up in db, not constant time. bad look for CTF framework
func (ul *UserList) GetUserByWebToken(tasktoken string) (ui *User, err error) {
	tasktoken = strings.TrimSpace(tasktoken)
	if len(tasktoken) == 0 {
		return nil, errors.New("No token provided")
	}

	ul.lock.Lock()
	ui, found := ul.usersByWebtoken[tasktoken]
	ul.lock.Unlock()
	if found {
		return
	}

	// check task token
	tr := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    5 * time.Second,
		DisableCompression: true,
	}
	client := &http.Client{Transport: tr}
	resp, err := client.PostForm(os.Getenv("ORCA_HTTP_TOKEN_CHECKER"), url.Values{
		"token": {tasktoken},
	})

	if err != nil {
		return nil, errors.WithMessage(err, "http request failed")
	}
	switch resp.StatusCode {
	case 200:
	case 403:
		return nil, errors.New("Unknown token")
	default:
		return nil, errors.New("Server error")
	}
	// got result

	nameB, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.WithMessage(err, "http request failed")
	}
	name := strings.TrimSpace(string(nameB))

	ul.lock.Lock()
	defer ul.lock.Unlock()
	ui, found = ul.usersByWebtoken[tasktoken]
	if found {
		return ui, nil
	}
	ui, found = ul.users[name]
	if found {
		ui.lock.Lock()
		defer ui.lock.Unlock()
		ui.taskToken = tasktoken
		ul.usersByWebtoken[tasktoken] = ui
		return ui, nil
	}

	ui = &User{
		ID:        name,
		taskToken: tasktoken,
	}
	ul.users[name] = ui
	ul.usersByWebtoken[tasktoken] = ui
	return ui, nil
}

// This is a mess... This func is called with authorized user id via ldap rpc
// TODO: restructure auth process, decouple auth methods from handlers in main
// e.g.: web auth also could be performed via login/password
func (ul *UserList) GetUserFromSSH(uid string) (ui *User, err error) {
	ul.lock.Lock()
	defer ul.lock.Unlock()
	ui, found := ul.users[uid]
	if !found {
		ui = &User{
			ID: uid,
		}
		ul.users[uid] = ui
	}
	return ui, nil
}
