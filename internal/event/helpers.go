package event

// Publish emits e on bus. If bus is nil, this is a no-op.
func Publish(bus *Bus, e Event) {
	if bus == nil {
		return
	}
	bus.Publish(e)
}
