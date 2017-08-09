package pool

import (
	"github.com/NebulousLabs/Sia/types"
)

//
// A Worker is an instance of one miner.  A Client often represents a user and the worker represents a single miner.  There
// is a one to many client worker relationship
//
type Worker struct {
	WorkerID          uint64
	WorkerName        string
	Parent            *Client
	SharesThisSession uint64
}

func newWorker(c *Client, name string) (*Worker, error) {
	id := c.Pool.newStratumID()
	w := &Worker{
		WorkerID:          id(),
		WorkerName:        name,
		Parent:            c,
		SharesThisSession: 0,
	}
	return w, nil
}

func (w *Worker) printID() string {
	return sPrintID(w.WorkerID)
}

//
// A Client represents a user and may have one or more workers associated with it.  It is primarily used for
// accounting and statistics.
//
type Client struct {
	ClientID uint64           `json:"clientid"`
	Wallet   types.UnlockHash `json:"wallet"`
	Pool     *Pool
	Workers  map[string]*Worker `map:"map[ip]*Worker"`
}

// newClient creates a new Client record
func newClient(p *Pool) (*Client, error) {
	id := p.newStratumID()
	c := &Client{
		ClientID: id(),
		Pool:     p,
		Workers:  make(map[string]*Worker),
	}
	return c, nil
}

func findClient(p *Pool, name string) *Client {
	c := p.clients[name]
	return c
}

func (c *Client) addWallet(w types.UnlockHash) {
	c.Wallet = w
}

func (c *Client) addWorker(w *Worker) {
	c.Workers[w.WorkerName] = w
}

func (c *Client) printID() string {
	return sPrintID(c.ClientID)
}
