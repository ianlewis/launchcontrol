// Copyright 2013 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package launchcontrol provides interfaces to talk to
// Novation LaunchControl via MIDI in and out.
package launchcontrol

import (
	"errors"
	"strings"
	"time"

	"github.com/rakyll/portmidi"
)

// LaunchControl represents a device with an input and output MIDI stream.
type LaunchControl struct {
	inputStream  *portmidi.Stream
	outputStream *portmidi.Stream
}

const (
	// Dials at the top
	SendADial     = 0
	SendBDial     = 1
	PanDeviceDial = 2
	// Sliders in the middle
	Slider = 3
	// Track Focus & Track Control buttons near the bottom
	TrackFocusButton   = 4
	TrackControlButton = 5
	// Control buttons along the side
	SendSelectUpButton    = 6
	SendSelectDownButton  = 7
	TrackSelectPrevButton = 8
	TrackSelectNextButton = 9
	DeviceButton          = 10
	MuteButton            = 11
	SoloButton            = 12
	RecordArmButton       = 13
)

var TRACK_FOCUS_BTN = []int64{0x29, 0x2A, 0x2B, 0x2C, 0x39, 0x3A, 0x3B, 0x3C}
var TRACK_CONTROL_BTN = []int64{0x49, 0x4A, 0x4B, 0x4C, 0x59, 0x5A, 0x5B, 0x5C}
var CONTROL_BTN_MAP = map[int]int{
	104: SendSelectUpButton,
	105: SendSelectDownButton,
	106: TrackSelectPrevButton,
	107: TrackSelectNextButton,
}

// Hit represents physical touches to LaunchControl buttons.
type Event struct {
	Control int
	X       int
	Value   int64
}

// Open opens a connection LaunchControl and initializes an input and output
// stream to the currently connected device. If there are no
// devices are connected, it returns an error.
func Open() (*LaunchControl, error) {
	input, output, err := discover()
	if err != nil {
		return nil, err
	}

	var inStream, outStream *portmidi.Stream
	if inStream, err = portmidi.NewInputStream(input, 1024); err != nil {
		return nil, err
	}
	if outStream, err = portmidi.NewOutputStream(output, 1024, 0); err != nil {
		return nil, err
	}
	return &LaunchControl{inputStream: inStream, outputStream: outStream}, nil
}

// Listen listens the input stream for hits.
func (l *LaunchControl) Listen() <-chan Event {
	ch := make(chan Event)
	go func(lc *LaunchControl, ch chan Event) {
		for {
			// sleep for a while before the new polling tick,
			// otherwise operation is too intensive and blocking
			time.Sleep(10 * time.Millisecond)
			events, err := lc.Read()
			if err != nil {
				continue
			}
			for i := range events {
				ch <- events[i]
			}
		}
	}(l, ch)
	return ch
}

func contains(s []int64, e int64) int {
	for i, a := range s {
		if a == e {
			return i
		}
	}
	return -1
}

// Read reads events from the input stream. It returns max 64 events for each read.
func (l *LaunchControl) Read() (events []Event, err error) {
	var evts []portmidi.Event
	if evts, err = l.inputStream.Read(1024); err != nil {
		return
	}
	for _, evt := range evts {
		var ctl int
		var x int
		var value int64

		switch evt.Status {
		case 0x90, 0x80:
			// Button down
			if evt.Status == 0x90 {
				value = 1
			}

			if i := contains(TRACK_FOCUS_BTN, evt.Data1); i != -1 {
				ctl = TrackFocusButton
				x = i
			}
			if i := contains(TRACK_CONTROL_BTN, evt.Data1); i != -1 {
				ctl = TrackControlButton
				x = i
			}
			if evt.Data1 == 105 {
				ctl = DeviceButton
			}
			if evt.Data1 == 106 {
				ctl = MuteButton
			}
			if evt.Data1 == 107 {
				ctl = SoloButton
			}
			if evt.Data1 == 108 {
				ctl = RecordArmButton
			}
		case 0xB0:
			// Control
			if evt.Data1 >= 13 && evt.Data1 <= 20 {
				ctl = SendADial
				x = int(evt.Data1 - 13)
				value = evt.Data2
			}
			if evt.Data1 >= 29 && evt.Data1 <= 36 {
				ctl = SendBDial
				x = int(evt.Data1 - 29)
				value = evt.Data2
			}
			if evt.Data1 >= 49 && evt.Data1 <= 56 {
				ctl = PanDeviceDial
				x = int(evt.Data1 - 49)
				value = evt.Data2
			}
			if evt.Data1 >= 77 && evt.Data1 <= 84 {
				ctl = Slider
				x = int(evt.Data1 - 77)
				value = evt.Data2
			}
			if val, ok := CONTROL_BTN_MAP[int(evt.Data1)]; ok {
				ctl = val
				if evt.Data2 == 127 {
					value = 1
				}
			}
		}

		events = append(events, Event{
			Control: ctl,
			X:       x,
			Value:   value,
		})
	}
	return
}

// Light lights the button at x,y with the given greend and red values.
// c is the button type(TrackControlButton or TrackFocusButton),
// x is [0, 7], g and r are [0, 3]
func (l *LaunchControl) Light(c, x, g, r int) error {
	note := int64([]int{0x49, 0x4A, 0x4B, 0x4C}[x%4])
	if c == TrackFocusButton {
		note -= 0x20
	}
	if x > 3 {
		note += 0x10
	}
	velocity := int64(16*g + r + 8 + 4)
	err := l.outputStream.WriteShort(0x90, note, velocity)
	if err != nil {
		return err
	}
	// Sleep for 5 milliseconds. It seems that the Launch Control needs
	// time to finish commands or starts skipping them.
	time.Sleep(5 * time.Millisecond)
	return nil
}

// Turn off all buttons
func (l *LaunchControl) Reset() error {
	return l.outputStream.WriteShort(0xb0, 0, 0)
}

func (l *LaunchControl) Close() error {
	l.inputStream.Close()
	l.outputStream.Close()
	return nil
}

// discovers the currently connected LaunchControl device
// as a MIDI device.
func discover() (input portmidi.DeviceId, output portmidi.DeviceId, err error) {
	in := -1
	out := -1
	for i := 0; i < portmidi.CountDevices(); i++ {
		info := portmidi.GetDeviceInfo(portmidi.DeviceId(i))
		if strings.Contains(info.Name, "Launch Control XL") {
			if info.IsInputAvailable {
				in = i
			}
			if info.IsOutputAvailable {
				out = i
			}
		}
	}
	if in == -1 || out == -1 {
		err = errors.New("launchcontrol: no launch control is connected")
	} else {
		input = portmidi.DeviceId(in)
		output = portmidi.DeviceId(out)
	}
	return
}
