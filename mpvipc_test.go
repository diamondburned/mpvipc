package mpvipc

import (
	"fmt"
	"log"
	"time"
)

func ExampleConnection_Call() {
	conn := NewConnection("/tmp/mpv_socket")

	if err := conn.Open(); err != nil {
		log.Fatalln(err)
	}
	defer conn.Close()

	// toggle play/pause
	_, err := conn.Call("cycle", "pause")
	if err != nil {
		log.Fatalln(err)
	}

	// increase volume by 5
	_, err = conn.Call("add", "volume", 5)
	if err != nil {
		log.Fatalln(err)
	}

	// decrease volume by 3, showing an osd message and progress bar
	_, err = conn.Call("osd-msg-bar", "add", "volume", -3)
	if err != nil {
		log.Fatalln(err)
	}

	// get mpv's version
	version, err := conn.Call("get_version")
	if err != nil {
		log.Fatalln(err)
	}
	fmt.Printf("version: %f\n", version.(float64))
}

func ExampleConnection_Set() {
	conn := NewConnection("/tmp/mpv_socket")
	if err := conn.Open(); err != nil {
		log.Fatalln(err)
	}
	defer conn.Close()

	// pause playback
	if err := conn.Set("pause", true); err != nil {
		log.Fatalln(err)
	}

	// seek to the middle of file
	if err := conn.Set("percent-pos", 50); err != nil {
		log.Fatalln(err)
	}
}

func ExampleConnection_Get() {
	conn := NewConnection("/tmp/mpv_socket")

	if err := conn.Open(); err != nil {
		log.Fatalln(err)
	}
	defer conn.Close()

	// see if we're paused
	paused, err := conn.Get("pause")
	if err != nil {
		log.Fatalln(err)
	}

	if paused.(bool) {
		fmt.Printf("we're paused!\n")
	} else {
		fmt.Printf("we're not paused.\n")
	}

	// see the current position in the file
	elapsed, err := conn.Get("time-pos")
	if err != nil {
		log.Fatalln(err)
	}

	fmt.Printf("seconds from start of video: %f\n", elapsed.(float64))
}

func ExampleConnection_ListenForEvents() {
	conn := NewConnection("/tmp/mpv_socket")

	if err := conn.Open(); err != nil {
		log.Fatalln(err)
	}
	defer conn.Close()

	_, err := conn.Call("observe_property", 42, "volume")
	if err != nil {
		log.Fatalln(err)
	}

	events := make(chan *Event, 1)
	time5s := time.Tick(5 * time.Second)

	cancel := conn.ListenForEvents(func(ev *Event) { events <- ev })
	defer cancel()

	for {
		select {
		case event := <-events:
			if event.ID == 42 {
				fmt.Printf("volume now is %f\n", event.Data.(float64))
			} else {
				fmt.Printf("received event: %s\n", event.Name)
			}
		case <-time5s:
			return
		}
	}
}
