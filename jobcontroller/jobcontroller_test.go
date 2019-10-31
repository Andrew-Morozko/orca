package jobcontroller

// import (
// 	"context"
// 	"math/rand"
// 	"testing"
// 	"time"
// )

// const maxDelay = 100
// const minDelay = 10

// func TestJobcontroller(t *testing.T) {
// 	ctx, demandShutdown := context.WithCancel(context.Background())
// 	jc, requestShutdown := New(ctx)
// 	// seq := make(chan string, 10)
// 	go func() {
// 		t.Log("Job started")
// 		jc.Add(1)
// 		defer func() {
// 			t.Log("Job done")
// 			jc.Done()
// 		}()

// 		for {
// 			select {
// 			case <-jc.ShutdownRequest():
// 				t.Log("Shutdown requested")

// 			case <-jc.ShutdownDemand():
// 				t.Log("Shutdown demanded")
// 				return
// 			}
// 		}
// 	}()

// 	time.Sleep(time.Duration(rand.Intn(maxDelay-minDelay)+minDelay) * time.Microsecond)
// 	t.Log("Sent shutdown request")
// 	requestShutdown()
// 	time.Sleep(time.Duration(rand.Intn(maxDelay-minDelay)+minDelay) * time.Microsecond)
// 	t.Log("Sent shutdown demand")
// 	demandShutdown()
// 	jc.WaitForShutdown()
// 	t.Log("Shutdown done")

// 	// go func() {
// 	// 	for i := 0; i < 7; i++ {
// 	// 		t.Log(<-seq)
// 	// 	}
// 	// }()
// 	time.Sleep(time.Duration(maxDelay*5) * time.Microsecond)
// 	t.FailNow()
// }
