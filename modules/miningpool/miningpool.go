// Package pool is an implementation of the pool module, and is responsible for
// creating a mining pool, accepting incoming potential block solutions and
// rewarding the submitters proportionally for their shares.
package pool

// TODO: everything

import (
	"errors"
	"fmt"
	"math/rand"
	"net"
	"path/filepath"
	"sync"
	"time"

	"github.com/NebulousLabs/Sia/build"
	"github.com/NebulousLabs/Sia/crypto"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/persist"
	siasync "github.com/NebulousLabs/Sia/sync"
	"github.com/NebulousLabs/Sia/types"
)

const (
	// Names of the various persistent files in the pool.
	dbFilename   = modules.PoolDir + ".db"
	logFile      = modules.PoolDir + ".log"
	settingsFile = modules.PoolDir + ".json"
)

var (
	// dbMetadata is a header that gets put into the database to identify a
	// version and indicate that the database holds pool information.
	dbMetadata = persist.Metadata{
		Header:  "Sia Pool DB",
		Version: "0.0.1",
	}

	// persistMetadata is the header that gets written to the persist file, and is
	// used to recognize other persist files.
	persistMetadata = persist.Metadata{
		Header:  "Sia Pool",
		Version: "0.0.1",
	}

	// errPoolClosed gets returned when a call is rejected due to the pool
	// having been closed.
	errPoolClosed = errors.New("call is disabled because the pool is closed")

	// Nil dependency errors.
	errNilCS    = errors.New("pool cannot use a nil consensus state")
	errNilTpool = errors.New("pool cannot use a nil transaction pool")
	//	errNilWallet = errors.New("pool cannot use a nil wallet")

	// Required settings to run pool
	errNoAddressSet = errors.New("pool operators address must be set")

	miningpool bool  // indicates if the mining pool is actually running
	hashRate   int64 // indicates hashes per second
	// HeaderMemory is the number of previous calls to 'header'
	// that are remembered. Additionally, 'header' will only poll for a
	// new block every 'headerMemory / blockMemory' times it is
	// called. This reduces the amount of memory used, but comes at the cost of
	// not always having the most recent transactions.
	HeaderMemory = build.Select(build.Var{
		Standard: 10000,
		Dev:      500,
		Testing:  50,
	}).(int)

	// BlockMemory is the maximum number of blocks the miner will store
	// Blocks take up to 2 megabytes of memory, which is why this number is
	// limited.
	BlockMemory = build.Select(build.Var{
		Standard: 50,
		Dev:      10,
		Testing:  5,
	}).(int)

	// MaxSourceBlockAge is the maximum amount of time that is allowed to
	// elapse between generating source blocks.
	MaxSourceBlockAge = build.Select(build.Var{
		Standard: 30 * time.Second,
		Dev:      5 * time.Second,
		Testing:  1 * time.Second,
	}).(time.Duration)
)

// splitSet defines a transaction set that can be added componenet-wise to a
// block. It's split because it doesn't necessarily represent the full set
// prpovided by the transaction pool. Splits can be sorted so that the largest
// and most valuable sets can be selected when picking transactions.
type splitSet struct {
	averageFee   types.Currency
	size         uint64
	transactions []types.Transaction
}

type splitSetID int

// A Pool contains all the fields necessary for storing status for clients and
// performing the evaluation and rewarding on submitted shares
type Pool struct {
	// BlockManager variables. Becaues blocks are large, one block is used to
	// make many headers which can be used by miners. Headers include an
	// arbitrary data transaction (appended to the block) to make the merkle
	// roots unique (preventing miners from doing redundant work). Every N
	// requests or M seconds, a new block is used to create headers.
	//
	// Only 'blocksMemory' blocks are kept in memory at a time, which
	// keeps ram usage reasonable. Miners may request many headers in parallel,
	// and thus may be working on different blocks. When they submit the solved
	// header to the block manager, the rest of the block needs to be found in
	// a lookup.
	blockMem        map[types.BlockHeader]*types.Block             // Mappings from headers to the blocks they are derived from.
	arbDataMem      map[types.BlockHeader][crypto.EntropySize]byte // Mappings from the headers to their unique arb data.
	headerMem       []types.BlockHeader                            // A circular list of headers that have been given out from the api recently.
	sourceBlock     *types.Block                                   // The block from which new headers for mining are created.
	sourceBlockTime time.Time                                      // How long headers have been using the same block (different from 'recent block').
	memProgress     int                                            // The index of the most recent header used in headerMem.

	// Transaction pool variables.
	fullSets        map[modules.TransactionSetID][]int
	blockMapHeap    *mapHeap
	overflowMapHeap *mapHeap
	setCounter      int
	splitSets       map[splitSetID]*splitSet

	// Dependencies.
	cs     modules.ConsensusSet
	tpool  modules.TransactionPool
	wallet modules.Wallet
	dependencies
	modules.StorageManager

	// Pool ACID fields - these fields need to be updated in serial, ACID
	// transactions.
	announceConfirmed bool
	secretKey         crypto.SecretKey
	clients           map[string]*Client
	BlocksFound       []*Accounting
	// Pool transient fields - these fields are either determined at startup or
	// otherwise are not critical to always be correct.
	workingStatus        modules.PoolWorkingStatus
	connectabilityStatus modules.PoolConnectabilityStatus

	// Utilities.
	db           *persist.BoltDatabase
	listener     net.Listener
	log          *persist.Logger
	mu           sync.RWMutex
	persistDir   string
	port         string
	tg           siasync.ThreadGroup
	persist      persistence
	dispatcher   *Dispatcher
	stratumID    uint64
	blockCounter uint64
}

// checkUnlockHash will check that the pool has an unlock hash. If the pool
// does not have an unlock hash, an attempt will be made to get an unlock hash
// from the wallet. That may fail due to the wallet being locked, in which case
// an error is returned.
func (p *Pool) checkUnlockHash() error {
	if p.persist.GetUnlockHash() == (types.UnlockHash{}) {
		uc, err := p.wallet.NextAddress()
		if err != nil {
			return err
		}

		// Set the unlock hash and save the pool. Saving is important, because
		// the pool will be using this unlock hash to establish identity, and
		// losing it will mean silently losing part of the pool identity.
		p.persist.SetUnlockHash(uc.UnlockHash())
		err = p.saveSync()
		if err != nil {
			return err
		}
	}
	return nil
}

// startupRescan will rescan the blockchain in the event that the pool
// persistence layer has become desynchronized from the consensus persistence
// layer. This might happen if a user replaces any of the folders with backups
// or deletes any of the folders.
func (p *Pool) startupRescan() error {
	// Reset all of the variables that have relevance to the consensus set. The
	// operations are wrapped by an anonymous function so that the locking can
	// be handled using a defer statement.
	err := func() error {
		//		p.log.Debugf("Waiting to lock pool\n")
		p.mu.Lock()
		defer func() {
			//			p.log.Debugf("Unlocking pool\n")
			p.mu.Unlock()
		}()

		p.log.Println("Performing a pool rescan.")
		p.persist.SetRecentChange(modules.ConsensusChangeBeginning)
		p.persist.SetBlockHeight(0)
		p.persist.SetTarget(types.Target{})
		return p.saveSync()
	}()
	if err != nil {
		return err
	}

	// Subscribe to the consensus set. This is a blocking call that will not
	// return until the pool has fully caught up to the current block.
	err = p.cs.ConsensusSetSubscribe(p, modules.ConsensusChangeBeginning, p.tg.StopChan())
	if err != nil {
		return err
	}
	p.tg.OnStop(func() {
		p.cs.Unsubscribe(p)
	})
	return nil
}

// newPool returns an initialized Pool, taking a set of dependencies as input.
// By making the dependencies an argument of the 'new' call, the pool can be
// mocked such that the dependencies can return unexpected errors or unique
// behaviors during testing, enabling easier testing of the failure modes of
// the Pool.
func newPool(dependencies dependencies, cs modules.ConsensusSet, tpool modules.TransactionPool, wallet modules.Wallet, listenerAddress string, persistDir string) (*Pool, error) {
	// Check that all the dependencies were provided.
	if cs == nil {
		return nil, errNilCS
	}
	if tpool == nil {
		return nil, errNilTpool
	}
	// if wallet == nil {
	// 	return nil, errNilWallet
	// }

	// Create the pool object.
	p := &Pool{
		cs:    cs,
		tpool: tpool,
		//		wallet:       wallet,
		dependencies: dependencies,
		blockMem:     make(map[types.BlockHeader]*types.Block),
		arbDataMem:   make(map[types.BlockHeader][crypto.EntropySize]byte),
		headerMem:    make([]types.BlockHeader, HeaderMemory),

		clients:   make(map[string]*Client),
		fullSets:  make(map[modules.TransactionSetID][]int),
		splitSets: make(map[splitSetID]*splitSet),
		blockMapHeap: &mapHeap{
			selectID: make(map[splitSetID]*mapElement),
			data:     nil,
			minHeap:  true,
		},
		overflowMapHeap: &mapHeap{
			selectID: make(map[splitSetID]*mapElement),
			data:     nil,
			minHeap:  false,
		},

		persistDir: persistDir,
		stratumID:  rand.Uint64(),
	}
	// TODO: Look at errors.go in modules/host directory for hints
	// Call stop in the event of a partial startup.
	var err error
	// defer func() {
	// 	if err != nil {
	// 		err = composeErrors(p.tg.Stop(), err)
	// 	}
	// }()

	// Create the perist directory if it does not yet exist.
	err = dependencies.mkdirAll(p.persistDir, 0700)
	if err != nil {
		return nil, err
	}

	// Initialize the logger, and set up the stop call that will close the
	// logger.
	p.log, err = dependencies.newLogger(filepath.Join(p.persistDir, logFile))
	if err != nil {
		return nil, err
	}
	p.tg.AfterStop(func() {
		err = p.log.Close()
		if err != nil {
			// State of the logger is uncertain, a Println will have to
			// suffice.
			fmt.Println("Error when closing the logger:", err)
		}
	})

	// Load the prior persistence structures, and configure the pool to save
	// before shutting down.
	err = p.load()
	if err != nil {
		return nil, err
	}

	p.tg.AfterStop(func() {
		err = p.saveSync()
		if err != nil {
			p.log.Println("Could not save pool upon shutdown:", err)
		}
	})
	err = p.cs.ConsensusSetSubscribe(p, p.persist.RecentChange, p.tg.StopChan())
	if err == modules.ErrInvalidConsensusChangeID {
		// Perform a rescan of the consensus set if the change id is not found.
		// The id will only be not found if there has been desynchronization
		// between the miner and the consensus package.
		err = p.startupRescan()
		if err != nil {
			return nil, errors.New("mining pool startup failed - rescanning failed: " + err.Error())
		}
	} else if err != nil {
		return nil, errors.New("mining pool subscription failed: " + err.Error())
	}
	p.tg.OnStop(func() {
		p.cs.Unsubscribe(p)
	})

	p.tpool.TransactionPoolSubscribe(p)
	p.tg.OnStop(func() {
		p.tpool.Unsubscribe(p)
	})
	fmt.Println("      Starting Stratum Server")

	p.dispatcher = &Dispatcher{handlers: make(map[string]*Handler), mu: sync.RWMutex{}, p: p}
	p.dispatcher.log, err = dependencies.newLogger(filepath.Join(p.persistDir, "stratum.log"))

	// la := modules.NetAddress(listenerAddress)
	// p.dispatcher.ListenHandlers(la.Port())
	port := fmt.Sprintf("%d", p.InternalSettings().PoolNetworkPort)
	go p.dispatcher.ListenHandlers(port) // This will become a persistent config option
	p.tg.OnStop(func() {
		p.dispatcher.ln.Close()
	})

	return p, nil
}

// New returns an initialized Pool.
func New(cs modules.ConsensusSet, tpool modules.TransactionPool, wallet modules.Wallet, address string, persistDir string) (*Pool, error) {
	return newPool(productionDependencies{}, cs, tpool, wallet, address, persistDir)
}

// Close shuts down the pool.
func (p *Pool) Close() error {
	return p.tg.Stop()
}

// StartPool starts the pool running
func (p *Pool) StartPool() {
	miningpool = true
}

// StopPool stops the pool running
func (p *Pool) StopPool() {
	miningpool = false
}

// GetRunning returns the running (or not) status of the pool
func (p *Pool) GetRunning() bool {
	return miningpool
}

// WorkingStatus returns the working state of the pool, where working is
// defined as having received more than workingStatusThreshold settings calls
// over the period of workingStatusFrequency.
func (p *Pool) WorkingStatus() modules.PoolWorkingStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.workingStatus
}

// ConnectabilityStatus returns the connectability state of the pool, whether
// the pool can connect to itself on its configured netaddress.
func (p *Pool) ConnectabilityStatus() modules.PoolConnectabilityStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connectabilityStatus
}

// MiningMetrics returns information about the financial commitments,
// rewards, and activities of the pool.
func (p *Pool) MiningMetrics() modules.PoolMiningMetrics {
	err := p.tg.Add()
	if err != nil {
		build.Critical("Call to MiningMetrics after close")
	}
	defer p.tg.Done()
	return p.persist.GetMiningMetrics()
}

// SetInternalSettings updates the pool's internal PoolInternalSettings object.
func (p *Pool) SetInternalSettings(settings modules.PoolInternalSettings) error {
	p.log.Debugf("Waiting to lock pool\n")
	p.mu.Lock()
	defer func() {
		p.log.Debugf("Unlocking pool\n")
		p.mu.Unlock()
	}()
	err := p.tg.Add()
	if err != nil {
		return err
	}
	defer p.tg.Done()

	// The pool should not be open for business if it does not have an
	// unlock hash.
	if settings.AcceptingShares {
		err := p.checkAddress()
		if err != nil {
			return errors.New("internal settings not updated, no operator wallet set: " + err.Error())
		}
	}

	p.persist.SetSettings(settings)
	p.persist.SetRevisionNumber(p.persist.GetRevisionNumber() + 1)

	err = p.saveSync()
	if err != nil {
		return errors.New("internal settings updated, but failed saving to disk: " + err.Error())
	}
	return nil
}

// InternalSettings returns the settings of a pool.
func (p *Pool) InternalSettings() modules.PoolInternalSettings {
	return p.persist.GetSettings()
}

func (p *Pool) AddClient(c *Client) {
	//	p.log.Debugf("Waiting to lock pool\n")
	p.mu.Lock()
	defer func() {
		//		p.log.Debugf("Unlocking pool\n")
		p.mu.Unlock()
	}()

	p.clients[c.Name()] = c
}

func (p *Pool) findClient(name string) *Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.clients[name]
	if ok {
		return c
	}
	return nil
}

func (p *Pool) FindClient(name string) *modules.PoolClients {
	c := p.findClient(name)
	if c != nil {
		cbf := uint64(0)
		var pw []modules.PoolWorkers
		for wn, w := range c.Workers() {
			worker := modules.PoolWorkers{
				WorkerName:               wn,
				LastShareDuration:        w.LastShareDuration(),
				LastShareTime:            w.LastShareTime(),
				SharesThisSession:        w.SharesThisSession(),
				InvalidSharesThisSession: w.InvalidSharesThisSession(),
				StaleSharesThisSession:   w.StaleSharesThisSession(),
				SharesThisBlock:          w.SharesThisBlock(),
				InvalidSharesThisBlock:   w.InvalidSharesThisBlock(),
				StaleSharesThisBlock:     w.StaleSharesThisBlock(),
				BlocksFound:              w.BlocksFound(),
			}
			cbf += w.BlocksFound()
			pw = append(pw, worker)
		}
		client := modules.PoolClients{
			ClientName:  name,
			BlocksMined: cbf,
			Workers:     pw,
		}
		return &client
	}
	return nil
}

func (p *Pool) ClientData() []modules.PoolClients {
	var pc []modules.PoolClients
	for cn, c := range p.clients {
		cbf := uint64(0)
		var pw []modules.PoolWorkers
		for wn, w := range c.Workers() {
			worker := modules.PoolWorkers{
				WorkerName:               wn,
				LastShareDuration:        w.LastShareDuration(),
				LastShareTime:            w.LastShareTime(),
				SharesThisSession:        w.SharesThisSession(),
				InvalidSharesThisSession: w.InvalidSharesThisSession(),
				StaleSharesThisSession:   w.StaleSharesThisSession(),
				SharesThisBlock:          w.SharesThisBlock(),
				InvalidSharesThisBlock:   w.InvalidSharesThisBlock(),
				StaleSharesThisBlock:     w.StaleSharesThisBlock(),
				BlocksFound:              w.BlocksFound(),
			}
			cbf += w.BlocksFound()
			pw = append(pw, worker)
		}
		client := modules.PoolClients{
			ClientName:  cn,
			BlocksMined: cbf,
			Workers:     pw,
		}
		pc = append(pc, client)
	}
	return pc
}

// checkAddress checks that the miner has an address, fetching an address from
// the wallet if not.
func (p *Pool) checkAddress() error {
	if p.InternalSettings().PoolOperatorWallet != (types.UnlockHash{}) {
		return nil
	}
	return errNoAddressSet
}
