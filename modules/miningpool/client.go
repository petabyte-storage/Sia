package pool

import (
	"sync"

	"github.com/NebulousLabs/Sia/types"
)

//
// A Client represents a user and may have one or more workers associated with it.  It is primarily used for
// accounting and statistics.
//
type Client struct {
	mu       sync.RWMutex
	clientID uint64
	name     string
	wallet   types.UnlockHash
	pool     *Pool
	workers  map[string]*Worker //worker name to worker pointer mapping
}

// newClient creates a new Client record
func newClient(p *Pool, name string) (*Client, error) {
	id := p.newStratumID()
	c := &Client{
		clientID: id(),
		name:     name,
		pool:     p,
		workers:  make(map[string]*Worker),
	}
	return c, nil
}

func (c *Client) Name() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.name
}

func (c *Client) SetName(n string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.name = n

}

func (c *Client) Wallet() *types.UnlockHash {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return &c.wallet
}

func (c *Client) addWallet(w types.UnlockHash) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wallet = w
}

func (c *Client) Pool() *Pool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pool
}
func (c *Client) Worker(wn string) *Worker {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.workers[wn]
}

//
// Workers returns a copy of the workers map.  Caution however since the map contains pointers to the actual live
// worker data
//
func (c *Client) Workers() map[string]*Worker {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.workers
}

func (c *Client) addWorker(w *Worker) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.workers[w.Name()] = w
}

func (c *Client) printID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return sPrintID(c.clientID)
}
