package events

import "testing"

func TestBroker(t *testing.T) {
	b := New()
	admin, _, stopA := b.Subscribe(func(string) bool { return true })
	viewer, behind, stopV := b.Subscribe(func(p string) bool { return p == "devices.read" })
	defer stopA()
	b.Publish(Event{Type: Device, Perm: "devices.read", Data: 1})
	b.Publish(Event{Type: Upgrade, Perm: "settings.manage", Data: 2})
	if e := <-admin; e.Type != Device {
		t.Fatal(e)
	}
	if e := <-admin; e.Type != Upgrade {
		t.Fatal(e)
	}
	if e := <-viewer; e.Type != Device {
		t.Fatal(e)
	}
	select {
	case e := <-viewer:
		t.Fatalf("viewer got %v", e)
	default:
	}
	// A subscriber that falls behind is told to resync once it drains.
	for i := 0; i < queue+10; i++ {
		b.Publish(Event{Type: Device, Perm: "devices.read", Data: i})
	}
	if behind() {
		t.Fatal("behind before draining")
	}
	for i := 0; i < queue; i++ {
		<-viewer
	}
	if !behind() || behind() {
		t.Fatal("resync flag")
	}
	stopV()
	if b.Subscribers() != 1 {
		t.Fatal("unsubscribe")
	}
}
