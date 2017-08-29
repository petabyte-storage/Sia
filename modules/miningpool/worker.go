package pool

import (
	"sync"
	"time"

	"github.com/NebulousLabs/Sia/types"
)

const (
	numSharesToAverage = 20
)

//
// A Worker is an instance of one miner.  A Client often represents a user and the worker represents a single miner.  There
// is a one to many client worker relationship
//
type Worker struct {
	mu                       sync.RWMutex
	workerID                 uint64
	name                     string
	parent                   *Client
	sharesThisSession        uint64
	invalidSharesThisSession uint64
	staleSharesThisSession   uint64
	sharesThisBlock          uint64
	invalidSharesThisBlock   uint64
	staleSharesThisBlock     uint64
	blocksFound              uint64
	shareTimes               [numSharesToAverage]time.Time
	lastShareTime            uint8
	currentDifficulty        types.Currency
}

func newWorker(c *Client, name string) (*Worker, error) {
	id := c.Pool().newStratumID()
	w := &Worker{
		workerID:                 id(),
		name:                     name,
		parent:                   c,
		sharesThisSession:        0,
		invalidSharesThisSession: 0,
		staleSharesThisSession:   0,
		sharesThisBlock:          0,
		invalidSharesThisBlock:   0,
		staleSharesThisBlock:     0,
		blocksFound:              0,
	}
	return w, nil
}

func (w *Worker) printID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return sPrintID(w.workerID)
}

func (w *Worker) Name() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.name
}

func (w *Worker) SetName(n string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.name = n
}

func (w *Worker) Parent() *Client {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.parent
}

func (w *Worker) SetParent(p *Client) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.parent = p
}

func (w *Worker) SharesThisSession() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.sharesThisSession
}

func (w *Worker) ClearSharesThisSession() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sharesThisSession = 0
}

func (w *Worker) IncrementSharesThisSession() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sharesThisSession++
}

func (w *Worker) SharesThisBlock() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.sharesThisBlock
}

func (w *Worker) ClearSharesThisBlock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sharesThisBlock = 0
}

func (w *Worker) IncrementSharesThisBlock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sharesThisBlock++
}

func (w *Worker) InvalidSharesThisSession() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.invalidSharesThisSession
}

func (w *Worker) ClearInvalidSharesThisSession() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalidSharesThisSession = 0
}

func (w *Worker) IncrementInvalidSharesThisSessin() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalidSharesThisSession++
}

func (w *Worker) InvalidSharesThisBlock() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.invalidSharesThisBlock
}

func (w *Worker) ClearInvalidSharesThisBlock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalidSharesThisBlock = 0
}

func (w *Worker) IncrementInvalidSharesThisBlock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalidSharesThisBlock++
}

func (w *Worker) StaleSharesThisSession() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.staleSharesThisSession
}

func (w *Worker) ClearStaleSharesThisSession() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.staleSharesThisSession = 0
}

func (w *Worker) IncrementStaleSharesThisSession() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.staleSharesThisSession++
}

func (w *Worker) StaleSharesThisBlock() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.staleSharesThisBlock
}

func (w *Worker) ClearStaleSharesThisBlock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.staleSharesThisBlock = 0
}

func (w *Worker) IncrementStaleSharesThisBlock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.staleSharesThisBlock++
}
func (w *Worker) BlocksFound() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.blocksFound
}

func (w *Worker) ClearBlocksFound() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blocksFound = 0
}

func (w *Worker) IncrementBlocksFound() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blocksFound++
}

func (w *Worker) LastShareTime() time.Time {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.shareTimes[w.lastShareTime]
}

func (w *Worker) SetLastShareTime(t time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastShareTime++
	if w.lastShareTime == numSharesToAverage {
		w.lastShareTime = 0
	}
	//	fmt.Printf("Shares per minute: %.2f\n", w.ShareRate()*60)
	w.shareTimes[w.lastShareTime] = t
}

// ShareRate returns shares per second
func (w *Worker) ShareRate() float32 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	oldestTime := time.Now()
	for _, e := range w.shareTimes {
		if e.Before(oldestTime) {
			oldestTime = e
		}
	}
	howLong := time.Now().Sub(oldestTime)
	return float32(numSharesToAverage / howLong.Seconds())

}
