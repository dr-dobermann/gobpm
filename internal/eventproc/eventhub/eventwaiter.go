package eventhub

type eventWaiter interface {
	Service() error
}
