package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"

	"github.com/Andrew-Morozko/orca/ldap/ldaplogin"
	"github.com/Andrew-Morozko/orca/mylog"
	"github.com/Andrew-Morozko/orca/orca"
	"github.com/Andrew-Morozko/orca/orca/errctrl"
	ioctrl "github.com/Andrew-Morozko/orca/orca/ioctrl"
	"github.com/Andrew-Morozko/orca/orca/mydocker"
	trie "github.com/Andrew-Morozko/orca/orca/search"
	orcassh "github.com/Andrew-Morozko/orca/orca/ssh"
	"github.com/facette/natsort"
	"google.golang.org/grpc"

	"github.com/Andrew-Morozko/orca/jobcontroller"

	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"io"
	"strings"

	"github.com/pkg/errors"

	"io/ioutil"

	"github.com/gliderlabs/ssh"
	"golang.org/x/crypto/ssh/terminal"
	// "github.com/docker/docker/pkg/stdcopy"
)

func getEnv(key string) string {
	val, found := os.LookupEnv(key)
	if !found {
		panic(fmt.Sprintf(`Env var named "%s" not found!`, key))
	}
	return val
}

var grpcServerAddr = getEnv("ORCA_GRPC_LDAP_SERVER")

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

var userlist = orca.NewUserList()

func webHandler(jc jobcontroller.JobController, shutdownReq <-chan struct{}) {
	jc.Job.Add(1)
	defer jc.Job.Done()
	jc = jc.AddLoggerPrefix("Web server")

	// jc = jc.AddLoggerPrefix("Director")
	rp := httputil.ReverseProxy{
		ErrorHandler: func(resp http.ResponseWriter, req *http.Request, err error) {
			action := req.Header.Get("OrcaRequestAction")
			jc.Logger.Log("Urlstring:", req.URL.String())
			if action == "" {
				return
			}
			req.Header.Del("OrcaRequestAction")
			switch action {
			case "redirectlogin":
				jc.Logger.Log("Redirecting user to login")
				redirectUrl := fmt.Sprintf(os.Getenv("ORCA_HTTP_LOGIN_URL"), url.QueryEscape("http://"+req.Host+req.RequestURI))
				http.Redirect(resp, req, redirectUrl, http.StatusFound)
			default:
				jc.Logger.Debug.Err(err, "Error handler error")
				http.Error(resp, action, http.StatusInternalServerError)
			}

		},
		Director: func(req *http.Request) {
			jc.Job.Add(1)
			defer jc.Job.Done()

			// jc.Logger.Log("got request", req)
			jc.Logger.Log("Got request for ", req.Host)
			// determine user identity
			// "ip"/"cookie"
			// todo: request to the server to authorize the provided cookie
			cookieName := os.Getenv("ORCA_HTTP_USER_IDENTITY_COOKIE")
			cookie, err := req.Cookie(cookieName)
			if err == http.ErrNoCookie {
				// send to page that redirects to login page
				req.Header.Add("OrcaRequestAction", "redirectlogin")
				return
			}

			ui, err := userlist.GetUserByWebToken(cookie.Value)
			if err != nil {
				req.Header.Add("OrcaRequestAction", "redirectlogin")
				return
			}
			// Remove auth cookie:
			cookies := req.Cookies()
			req.Header.Set("Cookie", "")
			for _, c := range cookies {
				if c.Name != cookieName {
					req.AddCookie(c)
				}
			}
			req.Header.Add("X-ORCA-USER-IDENTITY-TOKEN", cookie.Value)

			// TODO: configurable extraction of taskname from url
			taskName := strings.Split(req.Host, ".")[0]
			taskName = strings.ToLower(taskName)
			oi, err := imageList.GetImage(orca.ImageKindWeb, taskName, ui)

			switch err {
			case orca.ImageNotFoundErr:
				req.Header.Add("OrcaRequestAction", "Image not found")
				return
			case orca.ImageNotAvailibleErr:
				req.Header.Add("OrcaRequestAction", "Image not availible to you")
				return
			}

			jc.Logger.Log("Got web image ", oi.Name)
			jc.Logger.Log("Trying to get ContainerUser")
			var cu *orca.ContainerUser
			var oc *orca.Container
			var status orca.ContainerStatus
			for i := 1; i <= maxRestarts; i++ {
				cu = oi.GetContainerUser(jc, ui)
				cu.Activity()
				oc, status = cu.GetContainer()
				if status.ContainerState != orca.ContainerStateWorking {
					jc.Logger.Logf("Failed to get working container, got %s; retrying %d/%d", status, i, maxRestarts)
				} else {
					break
				}
			}
			if oc == nil {
				jc.Logger.Fatal.Logf("Failed to get working container, got %s and ran out of retries", status)
				req.Header.Add("OrcaRequestAction", "Failed to start the container")
				return //500
			}

			// kinda hacky, best do it when the page is sent, but whatever,
			// http is fast and this is insignificant.

			defer cu.NotifyConnectionClosed()

			// jc.Logger.Debug.Log("oc.id ", oc.DockerID)

			targetQuery := oc.URL.RawQuery
			tmpStr := req.RemoteAddr + " -> " + req.Host + " -> "
			req.URL.Scheme = oc.URL.Scheme
			req.URL.Host = oc.URL.Host
			req.URL.Path = singleJoiningSlash(oc.URL.Path, req.URL.Path)
			jc.Logger.Debug.Log(tmpStr, req.URL)

			if targetQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
			}
			if _, ok := req.Header["User-Agent"]; !ok {
				// explicitly disable User-Agent so it's not set to default value
				req.Header.Set("User-Agent", "")
			}

			// modded query to go to the container

		},
	}
	// rp.ServeHTTP
	s := http.Server{Addr: ":8080", Handler: &rp}

	go func() {
		jc.Job.Add(1)
		defer jc.Job.Done()
		jc.Logger.Log("Starting http server on ", s.Addr)
		go func() {
			// Attempt restarts if unexpected
			err := s.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				jc.Logger.Fatal.Err(err)
			}
		}()

		for {
			select {
			case <-shutdownReq:
				shutdownReq = nil
				err := s.Shutdown(jc.ShutdownCtx)
				if err == nil {
					return
				} else {
					jc.Logger.Fatal.Err(err)
				}
			case <-jc.Done():
				s.Close()
				return
			}
		}
	}()
}

var userFail = errors.New("User failed to select the task")

func sshMenu(rw io.ReadWriter, phs orcassh.PTYHandlerSetter, ui *orca.User) (oi *orca.Image, err error) {
	// if !isPty {
	// 	_, _ = io.WriteString(rw, "Can't work in a non-pty mode!\n")
	// 	return nil, errors.New("No PTY")
	// }
	term := terminal.NewTerminal(rw, "Select the task: ")

	// Update the terminal size

	phs(func(win ssh.Window) {
		_ = term.SetSize(win.Width, win.Height)
	})

	_, err = io.WriteString(term, "Availible tasks:\n\n")
	if err != nil {
		return
	}

	tasks := imageList.GetImages(orca.ImageKindSSH, ui)
	trie := trie.New()

	tasknames := make([]string, 0, len(tasks))
	for taskname := range tasks {
		tasknames = append(tasknames, taskname)
	}
	natsort.Sort(tasknames)

	for _, task := range tasknames {
		trie.Add(task)
		_, err = io.WriteString(term, task)
		if err != nil {
			return
		}
		_, err = io.WriteString(term, "\n")
		if err != nil {
			return
		}
	}
	_, err = io.WriteString(term, "\n")
	if err != nil {
		return
	}

	term.AutoCompleteCallback = func(line string, pos int, key rune) (newLine string, newPos int, ok bool) {
		switch key {
		case '\t':
			line = strings.ToLower(line)
			gc, em := trie.Search(line)
			if gc == "" {
				return line, pos, false
			}
			newLine = line + gc
			if em {
				newLine += " "
			}
			newPos = len(newLine)
			ok = true
			return

		default:
			// line = strings.ToLower(line)
			// gc, em := trie.Search(line)
			// if gc == "" {
			// 	return line, pos, false
			// }
			// newLine = line + gc
			// if em {
			// 	newLine += " "
			// }
			// newPos = len(line)
			// ok = true
			return line, pos, false
		}

	}

	var selectedTask string
	for attempt := 0; attempt < 3; attempt++ {
		selectedTask, err = term.ReadLine()
		if err != nil {
			return
		}
		selectedTask = strings.ToLower(strings.TrimSpace(selectedTask))

		if task, found := tasks[selectedTask]; found {
			return task, nil
		} else {
			_, err = io.WriteString(term, "Not found!\n")
			if err != nil {
				return
			}
		}
	}
	_, err = io.WriteString(term, "Failed to select the task\n")
	if err != nil {
		return
	}
	err = userFail
	return
}

func sshHandler(jc jobcontroller.JobController, sess *orcassh.SSHSession) {
	defer jc.Job.Done()
	jc = jc.NewCtx(sess.Context())
	jc = jc.AddLoggerPrefix("SSH handler")

	jc.Logger.Log("Got connection from ", sess.RemoteAddr())

	var err error
	status_ExitCode := 255
	defer func() {
		jc.Logger.Debug.Log("exit handler executing")
		if jc.ShutdownStatus() >= jobcontroller.Demanded {
			_, _ = io.WriteString(sess, ioctrl.BorderMessage(
				"Server is shutting down,",
				"sorry for the inconvenience",
			))
		} else {
			switch errors.Cause(err) {
			// expected errors, ignoring them
			case nil, userFail, io.EOF:
				// log.Println("Container exited with, status code =", status)
				err = nil
			case orca.InactivityTimeoutErr:
				_, _ = io.WriteString(sess, ioctrl.BorderMessage("Kicked out due to inactivity"))
				status_ExitCode = 254
			case orca.SessionTimeoutErr:
				_, _ = io.WriteString(sess, ioctrl.BorderMessage("Kicked out due to session age"))
				status_ExitCode = 254
			default:
				_, _ = io.WriteString(sess, ioctrl.BorderMessage(
					"Internal server error,",
					"sorry for the inconvenience",
				))
				jc.Logger.Err(err, "connection died")
				status_ExitCode = 255
			}
		}
		_ = sess.Exit(status_ExitCode)
	}()

	// determine user identity
	ui, ok := sess.Context().Value("User").(*orca.User)
	if !ok {
		err = errors.New("User is missing")
		return
	}

	jc = jc.AddLoggerPrefix(fmt.Sprintf(`user "%s"`, ui.ID))

	// Proxy data, this gives us ability to close p1/p2 without loosing
	// the client connection (for final error reporting)
	sessProxy := ioctrl.ProxyReadWriter(sess)
	go func() {
		<-jc.Done()
		sessProxy.Close()
	}()

	oi, err := sshMenu(sessProxy, sess.SetPTYHandler, ui)
	if err != nil {
		return
	}
	jc = jc.AddLoggerPrefix(oi.String())
	// _, err = io.WriteString(sess, "Launching...")
	// if err != nil {
	// 	return
	// }
	var cu *orca.ContainerUser
	var oc *orca.Container
	var status orca.ContainerStatus
	for i := 1; i <= maxRestarts; i++ {
		cu = oi.GetContainerUser(jc, ui)
		cu.Activity()
		oc, status = cu.GetContainer()
		if status.ContainerState != orca.ContainerStateWorking {
			jc.Logger.Logf("Failed to get working container, got %s; retrying %d/%d", status, i, maxRestarts)
		} else {
			break
		}
	}
	if oc == nil {
		jc.Logger.Fatal.Logf("Failed to get working container, got %s and ran out of retries", status)
		if status.Err != nil {
			err = status.Err
		} else {
			err = errors.New(status.String())
		}
		return
	}

	defer cu.NotifyConnectionClosed()
	// TODO: fails for multiuser containers (mirrors output), needs more complex logic
	// TODO: although, if you just attach to out and err - you have a way to monitor what
	// is happening in the container ;)
	stream, err := oc.GetStream(sess.Context())
	if err != nil {
		return
	}
	defer stream.Close()

	// RN we are pty-only, so the pty data must be simply sent over the connection
	// Some weird stuff happens if non-pty
	cm, err := ioctrl.NewCopyMonitor(sess.Context(), ioctrl.NotificationChanel(cu.ActivityChan()))
	if err != nil {
		return
	}

	cm.AddCopier(sessProxy, stream.Reader)
	cm.AddCopier(stream.Conn, sessProxy)

	sess.SetPTYHandler(func(win ssh.Window) {
		_ = oc.ResizeTTY(sess.Context(), win.Height, win.Width)
	})

	select {

	case exitStatus := <-cu.ShutdownDone():
		jc.Logger.Debug.Log("Exit status: ", exitStatus)
		switch exitStatus.ContainerState {
		case orca.ContainerStateShutdownInactivity, orca.ContainerStateShutdownSessionLen, orca.ContainerStateShutdownWithErr:
			err = exitStatus.Err

		case orca.ContainerStateShutdownWithErrMsg:
			err = exitStatus.Err
			status_ExitCode = int(exitStatus.Status)

		case orca.ContainerStateShutdown:
			status_ExitCode = int(exitStatus.Status)

		default:
			err = errors.New("Unexpected container exit status: " + exitStatus.String())
		}
	case <-cm.Done():
		jc.Logger.Log("IO closed")

		_, _, err = cm.Status()
		err = errors.WithMessage(err, "IO Closed")
	}
	return
}

const maxRestarts = 5

func setupSSHServer(jc jobcontroller.JobController, shutdownReq <-chan struct{}) (err error) {
	defer errctrl.Annotate(&err, "Failed to start ssh server")
	jc.Job.Add(1)
	defer jc.Job.Done()

	ldapConn, err := grpc.Dial(
		grpcServerAddr,
		grpc.WithInsecure(),
	)
	if err != nil {
		jc.Logger.Fatal.Err(err, "Can't connect to grpc-ldap-server")
		return
	}
	defer func() {
		if err != nil {
			ldapConn.Close()
		}
	}()

	ldapClient := ldaplogin.NewLDAPLoginClient(ldapConn)

	s := &ssh.Server{
		Addr: ":22222",
		PasswordHandler: func(ctx ssh.Context, pass string) (authorized bool) {
			reply, err := ldapClient.AuthPasswd(
				jc,
				&ldaplogin.PasswdAuthRequest{
					Login:    ctx.User(),
					Password: pass,
				},
			)
			if err != nil {
				jc.Logger.Err(err, "error in rpc call to AuthPasswd")
				return
			}
			switch reply.GetStatus() {
			case ldaplogin.AuthReply_OK:
			case ldaplogin.AuthReply_FAILED:
				jc.Logger.Logf(`User "%s" failed to pass password auth`, ctx.User())
				return
			case ldaplogin.AuthReply_SERVER_ERROR:
				jc.Logger.Error.Logf(`Auth server error on password login by "%s"`, ctx.User())
				return
			}
			ui, err := userlist.GetUserFromSSH(ctx.User())
			if err != nil {
				jc.Logger.Err(err, "error in while fetching UserIdentity from the list")
				return
			}
			ctx.SetValue(
				"User",
				ui,
			)
			return true

		},
		Handler: func(sess ssh.Session) {
			jc.Job.Add(1)
			sshHandler(jc, orcassh.Wrap(sess))
		},
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) (authorized bool) {
			reply, err := ldapClient.AuthKey(
				jc,
				&ldaplogin.KeyAuthRequest{
					Login:     ctx.User(),
					PublicKey: key.Marshal(),
				},
			)
			if err != nil {
				jc.Logger.Err(err, "error in rpc call to AuthKey")
				return
			}
			switch reply.GetStatus() {
			case ldaplogin.AuthReply_OK:
			case ldaplogin.AuthReply_FAILED:
				jc.Logger.Logf(`User "%s" failed to pass key auth`, ctx.User())
				return
			case ldaplogin.AuthReply_SERVER_ERROR:
				jc.Logger.Error.Logf(`Auth server error on key auth by "%s"`, ctx.User())
				return
			}

			ui, err := userlist.GetUserFromSSH(ctx.User())
			if err != nil {
				jc.Logger.Err(err, "error in while fetching UserIdentity from the list")
				return
			}
			ctx.SetValue(
				"User",
				ui,
			)
			return true
		},

		ConnCallback: nil, // optional callback for wrapping net.Conn before handling

	}
	jc.Logger.Log("Loading private keys:")

	files, err := filepath.Glob("./server_keys/id_*")
	if err != nil {
		return err
	}
	for _, filename := range files {
		fn := filepath.Base(filename)
		match, _ := filepath.Match("*.pub", fn)
		if match {
			continue
		}

		privkey, err := ioutil.ReadFile(filename)
		if err != nil {
			return err
		}

		err = s.SetOption(ssh.HostKeyPEM(privkey))
		if err != nil {
			return err
		}

		jc.Logger.Log(fn, " is loaded")
	}

	jc.Logger.Logf("Starting ssh server on %s", s.Addr)

	jc.Job.Add(1)
	go func() {
		defer jc.Job.Done()
		defer ldapConn.Close()

		var err error
		// Todo check time between exits, if < x - go away, else - continue retrying
		for restartNo := 1; restartNo <= maxRestarts; restartNo++ {
			err = s.ListenAndServe()
			if jc.IsShuttingDown() {
				return
			}
			jc.Logger.Warn.Err(err, "SSH server shutdown unexpectidly with error")
			jc.Logger.Logf("Attempting to restart the SSH server: %d/%d", restartNo, maxRestarts)
		}
		jc.Logger.Fatal.Err(err, "SSH server shut down: ran out of retries. Error")
	}()

	go func() {
		select {
		case <-shutdownReq:
			shutdownReq = nil
			err := s.Shutdown(jc)
			if err != nil {
				s.Close()
			}
			return
		case <-jc.ShutdownCtx.Done():
			s.Close()
			return
		}
	}()
	return
}

// Global to send it into various handlers
var imageList *orca.ImageList

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Println("Unexpected server shutdown!")
			log.Println("Panic reason: ", r)
			// panic(r)
			os.Exit(1)
		} else {
			log.Println("Expected server shut down. Bye!")
			os.Exit(0)
		}
	}()

	var err error

	logCtx, cancelLogCtx := context.WithCancel(context.Background())
	defer func() {
		time.Sleep(5 * time.Millisecond)
		cancelLogCtx()
	}()
	log, msgChan := mylog.NewBaseLogger(logCtx, mylog.Debug, 200)

	sc, err := jobcontroller.New(
		logCtx,
		jobcontroller.RequestDeadline(15*time.Minute),
		jobcontroller.DemandDeadline(5*time.Second),
		jobcontroller.InterruptHandler(log),
	)
	if err != nil {
		panic(err)
	}

	go func() {
		mw := mylog.NewMessageWriter(os.Stderr)

		for {
			msg, more := <-msgChan
			if !more {
				return
			}
			_ = mw.WriteMessage(msg)
			if msg.Level == mylog.Fatal {
				// Trying to reload server to undo whatever has gone wrong
				sc.Demand()
			}
		}
	}()

	log.Log("Starting Orca...")
	// Set global
	jc := sc.GetJobController(log)
	shutdownReq := sc.ShutdownRequested()

	// reloadChan := make(chan os.Signal, 1)
	// signal.Notify(reloadChan, syscall.SIGUSR1)

	// go func() {
	// 	for {
	// 		select {
	// 		case <-reloadChan:
	// 			// reload config/containers
	// 			// loadContainers(ctx, docker)
	// 			// TODO: reload containers on event.
	// 		}

	// 	}
	// }()

	orca.Docker, err = mydocker.FromEnv()
	if err != nil {
		log.Fatal.Err(err, "failed to get docker client")
		return
	}

	imageList, err = orca.NewImageList(jc)
	if err != nil {
		log.Fatal.Err(err, "failed to get docker client")
		return
	}
	log.Log("Setting up servers")

	err = setupSSHServer(jc, shutdownReq)
	if err != nil {
		log.Fatal.Err(err, "failed to start ssh server")
		return
	}

	webHandler(jc, shutdownReq)
	if err != nil {
		log.Fatal.Err(err, "failed to start web server")
		return
	}

	_ = ioutil.WriteFile("./orca.pid", []byte(fmt.Sprintf("%d", os.Getpid())), 0664)

	<-sc.Done()
	log.Log("Shutdown Completed")
}
