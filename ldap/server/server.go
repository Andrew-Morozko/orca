// gRPC server for performing auth via LDAP
//
package main

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"

	"log"
	"net"
	"strings"
	"time"

	"github.com/Andrew-Morozko/orca/ldap/exitlib"
	"github.com/Andrew-Morozko/orca/ldap/ldaplogin"
	"github.com/gliderlabs/ssh"
	"github.com/libp2p/go-reuseport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
	"gopkg.in/ldap.v3"
)

func getEnv(key string) string {
	val, found := os.LookupEnv(key)
	if !found {
		panic(fmt.Sprintf(`Env var named "%s" not found!`, key))
	}
	return val
}

// Stuff that should be in the configuration file, but I'm too lazy

var ldapServerAddr = getEnv("LDAP_SERVER_ADDR")
var ldapUser = getEnv("LDAP_ADMIN_USERNAME")
var ldapPassword = getEnv("LDAP_ADMIN_PASSWORD")

var ldapSearchLoc = getEnv("LDAP_SEARCH_LOC")
var ldapSearchQuery = getEnv("LDAP_SEARCH_QUERY")
var ldapSSHKeyFieldName = getEnv("LDAP_SSH_KEY_FIELD_NAME")
var ldapPasswordFieldName = getEnv("LDAP_PASSWORD_FIELD_NAME")

var grpcServerAddr = getEnv("LDAP_GRPC_SERVER_ADDR")
var whitelist = makeWhitelist(getEnv("LDAP_GRPC_WHITELIST"))

// /Stuff that should be in the configuration file, but I'm too lazy

type ldapLoginServer struct {
	ldaplogin.UnimplementedLDAPLoginServer
	ldapConn *ldap.Conn
}

const passwdPrefix = "{SSHA}"

func comparePassword(hashStr, password string) (equal bool, err error) {
	if hashStr[:len(passwdPrefix)] != passwdPrefix {
		err = errors.New("unknown hash type")
		return
	}

	data, err := base64.StdEncoding.DecodeString(hashStr[len(passwdPrefix):])
	if err != nil {
		return
	}

	salt := data[len(data)-4:]
	hash := data[:len(data)-4]

	sha := sha1.New()
	_, err = sha.Write([]byte(password))
	if err != nil {
		return
	}
	_, err = sha.Write(salt)
	if err != nil {
		return
	}
	sum := sha.Sum(nil)

	equal = subtle.ConstantTimeCompare(sum, hash) == 1
	return
}

func (s *ldapLoginServer) AuthPasswd(ctx context.Context, req *ldaplogin.PasswdAuthRequest) (resp *ldaplogin.AuthReply, err error) {
	defer func() {
		switch {
		case err != nil && resp == nil:
			log.Println("AuthPasswd error: ", err.Error())
			fallthrough
		case err == nil && resp == nil:
			resp = &ldaplogin.AuthReply{
				Status: ldaplogin.AuthReply_SERVER_ERROR,
			}
			fallthrough
		case resp != nil:
			err = nil
		}
	}()

	searchRequest := ldap.NewSearchRequest(
		ldapSearchLoc,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(ldapSearchQuery, ldap.EscapeFilter(req.GetLogin())),
		[]string{ldapPasswordFieldName},
		nil,
	)

	sr, err := s.ldapConn.Search(searchRequest)
	if err != nil {
		return
	}

	switch len(sr.Entries) {
	case 0:
		resp = &ldaplogin.AuthReply{
			Status: ldaplogin.AuthReply_FAILED,
		}
		return
	case 1:
		// correct case, do nothing
	default:
		err = errors.New(">1 user with same uid!")
		return
	}

	entry := sr.Entries[0]
	hashStr := entry.GetAttributeValue(ldapPasswordFieldName)
	authorized, err := comparePassword(hashStr, req.GetPassword())
	if err != nil {
		return
	}
	if !authorized {
		resp = &ldaplogin.AuthReply{
			Status: ldaplogin.AuthReply_FAILED,
		}
		return
	}

	resp = &ldaplogin.AuthReply{
		Status: ldaplogin.AuthReply_OK,
	}
	return
}

func (s *ldapLoginServer) AuthKey(ctx context.Context, req *ldaplogin.KeyAuthRequest) (resp *ldaplogin.AuthReply, err error) {
	defer func() {
		switch {
		case err != nil && resp == nil:
			log.Println("AuthKey error: ", err.Error())
			fallthrough
		case err == nil && resp == nil:
			resp = &ldaplogin.AuthReply{
				Status: ldaplogin.AuthReply_SERVER_ERROR,
			}
			fallthrough
		case resp != nil:
			err = nil
		}
	}()

	userKey, err := ssh.ParsePublicKey(req.GetPublicKey())
	if err != nil {
		log.Println("User-supplied key parse error: ", err.Error())
		resp = &ldaplogin.AuthReply{
			Status: ldaplogin.AuthReply_FAILED,
		}
		return
	}

	searchRequest := ldap.NewSearchRequest(
		ldapSearchLoc,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(ldapSearchQuery, ldap.EscapeFilter(req.GetLogin())),
		[]string{ldapSSHKeyFieldName},
		nil,
	)

	sr, err := s.ldapConn.Search(searchRequest)
	if err != nil {
		return
	}

	switch len(sr.Entries) {
	case 0:
		resp = &ldaplogin.AuthReply{
			Status: ldaplogin.AuthReply_FAILED,
		}
		return
	case 1:
		// correct case, do nothing
	default:
		err = errors.New(">1 user with same uid!")
		return
	}

	entry := sr.Entries[0]
	publicKeys := entry.GetAttributeValues(ldapSSHKeyFieldName)

	var authorizedKey ssh.PublicKey

	for _, key := range publicKeys {
		// parse key (must be successful)
		authorizedKey, err = ssh.ParsePublicKey([]byte(key))
		if err != nil {
			// should be pre-checked before adding into the db!
			err = nil
			continue
		}
		if ssh.KeysEqual(userKey, authorizedKey) {
			resp = &ldaplogin.AuthReply{
				Status: ldaplogin.AuthReply_OK,
			}
			return
		}
	}

	resp = &ldaplogin.AuthReply{
		Status: ldaplogin.AuthReply_FAILED,
	}
	return
}

func retry(count int, delay time.Duration, f func() error) (err error) {
	for count != 0 {
		err = f()
		if err != nil {
			log.Println(err)
		} else {
			return
		}
		time.Sleep(delay)
		if count > 0 {
			count--
		}
	}
	return
}

func main() {
	defer exitlib.HandleExit()
	_ = ioutil.WriteFile("./ldap-grpc-srv.pid", []byte(fmt.Sprintf("%d", os.Getpid())), 0664)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	var ldapConn *ldap.Conn
	err := retry(10, time.Second, func() (err error) {
		ldapConn, err = ldap.Dial("tcp", ldapServerAddr)
		return err
	})
	if err != nil {
		exitlib.Exit("Failed to connect to ldap server")
	}
	defer ldapConn.Close()

	err = retry(10, time.Second, func() (err error) {
		err = ldapConn.Bind(ldapUser, ldapPassword)
		if ldaperr, ok := err.(*ldap.Error); ok {
			if ldaperr.ResultCode == 49 {
				exitlib.Exit("Invalid creds of the admin user")
			}
		}
		return
	})
	if err != nil {
		exitlib.Exit("Failed to bind to ldap server")
	}

	var grpcLis net.Listener
	err = retry(10, time.Second, func() (err error) {
		grpcLis, err = reuseport.Listen("tcp", grpcServerAddr)
		return
	})
	if err != nil {
		exitlib.Exit("Failed to create grpc listner")
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(ensureValidIP),
	)
	ldaplogin.RegisterLDAPLoginServer(
		grpcServer,
		&ldapLoginServer{
			ldapConn: ldapConn,
		},
	)
	go func() {
		<-sigChan
		grpcServer.GracefulStop()
	}()
	log.Println("Server started")
	err = grpcServer.Serve(grpcLis)
	log.Printf("Server ended: %s", err)
}

var errMissingPeerData = errors.New("peer data not found")
var errIPNotAllowed = errors.New("peer IP is not allowed")

type IPWhitelist []*net.IPNet

func makeWhitelist(ips string) (whitelist IPWhitelist) {
	exactIpMask := net.CIDRMask(32, 32)
	for _, ip := range strings.Split(ips, "\n") {
		// 127.0.0.1
		ip := strings.TrimSpace(ip)
		parsedIP := net.ParseIP(ip)
		if parsedIP != nil {
			ipnet := net.IPNet{
				IP:   parsedIP,
				Mask: exactIpMask,
			}
			whitelist = append(whitelist, &ipnet)
			continue
		}

		// 127.0.0.1/24
		_, ipnet, err := net.ParseCIDR(ip)
		if err == nil {
			whitelist = append(whitelist, ipnet)
		}
	}
	log.Println(whitelist)
	return
}

func (iwl IPWhitelist) Contains(ip net.IP) bool {
	for _, ipnet := range iwl {
		log.Println(ipnet)
		if ipnet.Contains(ip) {
			return true
		}
	}
	return false
}

func ensureValidIP(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, errMissingPeerData
	}

	ok = false
	log.Println("got connection from: ", p.Addr)
	switch addr := p.Addr.(type) {
	case *net.UDPAddr:
		ok = whitelist.Contains(addr.IP)
	case *net.TCPAddr:
		ok = whitelist.Contains(addr.IP)
	case *net.UnixAddr:
		ok = true
	}
	if !ok {
		return nil, errIPNotAllowed
	}
	return handler(ctx, req)
}
