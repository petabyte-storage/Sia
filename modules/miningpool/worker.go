package pool

import (
	"sync"
	"time"

	"github.com/NebulousLabs/Sia/types"
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
	sharesThisBlock          uint64
	invalidSharesThisBlock   uint64
	blocksFound              uint64
	lastShareTime            time.Time
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
		sharesThisBlock:          0,
		invalidSharesThisBlock:   0,
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

	return w.lastShareTime
}

func (w *Worker) SetLastShareTime(t time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastShareTime = t
}
