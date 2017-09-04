package pool

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/NebulousLabs/Sia/crypto"
	"github.com/NebulousLabs/Sia/encoding"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/persist"
	"github.com/NebulousLabs/Sia/types"
)

const (
	extraNonce2Size = 4
)

// StratumRequestMsg contains stratum request messages received over TCP
type StratumRequestMsg struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// StratumResponseMsg contains stratum response messages sent over TCP
type StratumResponseMsg struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

// Handler represents the status (open/closed) of each connection
type Handler struct {
	conn   net.Conn
	closed chan bool
	notify chan bool
	p      *Pool
	log    *persist.Logger
	s      *Session
}

const (
	blockTimeout = 5 * time.Second
	// This should represent the max number of pending notifications (new blocks found) within blockTimeout seconds
	// better to have too many, than not enough (causes a deadlock otherwise)
	numPendingNotifies = 20
)

// Listen listens on a connection for incoming data and acts on it
func (h *Handler) Listen() { // listen connection for incomming data
	defer h.conn.Close()
	err := h.p.tg.Add()
	if err != nil {
		// If this goroutine is not run before shutdown starts, this
		// codeblock is reachable.
		return
	}
	defer h.p.tg.Done()

	h.log.Println("New connection from " + h.conn.RemoteAddr().String())
	h.s, _ = newSession(h.p)
	h.log.Println("New sessioon: " + sPrintID(h.s.SessionID))
	dec := json.NewDecoder(h.conn)
	for {
		var m StratumRequestMsg
		// fmt.Printf("%s: Setting timeout for %v\n", h.s.printID(), time.Now().Add(blockTimeout))
		h.conn.SetReadDeadline(time.Now().Add(blockTimeout))
		select {
		case <-h.p.tg.StopChan():
			h.closed <- true // not closed until we return but we signal now so our parent knows
			return
		case <-h.notify:
			m.Method = "mining.notify"
		default:
			err := dec.Decode(&m)
			if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
				// fmt.Printf("%s: Harmless timeout occurred\n", h.s.printID())
				h.conn.SetReadDeadline(time.Time{})
				dec = json.NewDecoder(h.conn)
				continue
			} else if err != nil {
				if err == io.EOF {
					h.log.Println("End connection")
				}
				h.closed <- true // send to dispatcher, that connection is closed
				return
			}
		}
		// fmt.Printf("%s: Clearing timeout\n", h.s.printID())

		h.conn.SetReadDeadline(time.Time{})
		switch m.Method {
		case "mining.subscribe":
			h.handleStratumSubscribe(m)
		case "mining.authorize":
			h.handleStatumAuthorize(m)
			h.sendStratumNotify()
		case "mining.extranonce.subscribe":
			h.handleStratumNonceSubscribe(m)
		case "mining.submit":
			h.handleStratumSubmit(m)
		case "mining.notify":
			h.log.Printf("New block to mine on\n")
			h.sendStratumNotify()
		default:
			h.log.Debugln("Unknown stratum method: ", m.Method)
		}
	}
}

func (h *Handler) sendResponse(r StratumResponseMsg) {
	b, err := json.Marshal(r)
	if err != nil {
		h.log.Debugln("json marshal failed for id: ", r.ID, err)
	} else {
		_, err = h.conn.Write(b)
		if err != nil {
			h.log.Debugln("connection write failed for id: ", r.ID, err)
		}
		newline := []byte{'\n'}
		h.conn.Write(newline)
		//		h.log.Debugln(string(b))
	}
}
func (h *Handler) sendRequest(r StratumRequestMsg) {
	b, err := json.Marshal(r)
	if err != nil {
		h.log.Debugln("json marshal failed for id: ", r.ID, err)
	} else {
		_, err = h.conn.Write(b)
		if err != nil {
			h.log.Debugln("connection write failed for id: ", r.ID, err)
		}
		newline := []byte{'\n'}
		h.conn.Write(newline)
		h.log.Debugln(string(b))
	}
}

// handleStratumSubscribe message is the first message received and allows the pool to tell the miner
// the difficulty as well as notify, extranonse1 and extranonse2
//
// TODO: Pull the appropriate data from either in memory or persistent store as required
func (h *Handler) handleStratumSubscribe(m StratumRequestMsg) {
	r := StratumResponseMsg{ID: m.ID}

	//	diff := "b4b6693b72a50c7116db18d6497cac52"
	t, _ := h.p.persist.Target.Difficulty().Uint64()
	h.log.Debugf("Difficulty: %x\n", t)
	tb := make([]byte, 8)
	binary.LittleEndian.PutUint64(tb, t)
	diff := hex.EncodeToString(tb)
	notify := "ae6812eb4cd7735a302a8a9dd95cf71f"
	extranonce1 := h.s.printNonce()
	extranonce2 := extraNonce2Size
	raw := fmt.Sprintf(`[ [ ["mining.set_difficulty", "%s"], ["mining.notify", "%s"]], "%s", %d]`, diff, notify, extranonce1, extranonce2)
	r.Result = json.RawMessage(raw)
	r.Error = json.RawMessage(`null`)
	h.sendResponse(r)
}

// handleStratumAuthorize allows the pool to tie the miner connection to a particular user or wallet
//
// TODO: This has to tie to either a connection specific record, or relate to a backend user, worker, password store
func (h *Handler) handleStatumAuthorize(m StratumRequestMsg) {
	type params [2]string
	r := StratumResponseMsg{ID: m.ID}

	r.Result = json.RawMessage(`true`)
	r.Error = json.RawMessage(`null`)
	var p params
	switch m.Method {
	case "mining.authorize":
		//		p := new([]params)
		err := json.Unmarshal(m.Params, &p)
		if err != nil {
			h.log.Printf("Unable to parse mining.authorize params: %v\n", err)
			r.Result = json.RawMessage(`false`)
		}
		client := p[0]
		worker := ""
		if strings.Contains(client, ".") {
			s := strings.SplitN(client, ".", -1)
			client = s[0]
			worker = s[1]
		}
		c := h.p.findClient(client)
		if c == nil {
			c, _ = newClient(h.p, client)
			c.log.Printf("Adding new client: %s\n", client)
			h.p.AddClient(c)
			//
			// There is a race condition here which we can reduce/avoid by re-searching for the client once we
			// have added it.
			//
			c = h.p.findClient(client)
		}
		if c.Worker(worker) == nil {
			w, _ := newWorker(c, worker)
			c.addWorker(w)
			c.log.Printf("Adding new worker: %s\n", worker)
			w.log.Printf("Adding new worker: %s\n", worker)
		}
		h.log.Debugln("client = " + client + ", worker = " + worker)
		if c.Wallet().LoadString(c.Name()) != nil {
			r.Result = json.RawMessage(`false`)
			r.Error = json.RawMessage(`{"Client Name must be valid wallet address"}`)
			h.log.Println("Client Name must be valid wallet address. Client name is: " + c.Name())
		} else {
			h.s.addClient(c)
			h.s.addWorker(c.Worker(worker))
			h.s.CurrentWorker.log.Printf("Clearing share stats\n")
			h.s.CurrentWorker.ClearInvalidSharesThisSession()
			h.s.CurrentWorker.ClearStaleSharesThisSession()
			h.s.CurrentWorker.ClearSharesThisSession()
		}
		// TODO: figure out how to store this worker - probably in Session
	default:
		h.log.Debugln("Unknown stratum method: ", m.Method)
		r.Result = json.RawMessage(`false`)
	}

	h.sendResponse(r)
}

// handleStratumExtranonceSubscribe tells the pool that this client can handle the extranonce info
//
// TODO: Not sure we have to anything if all our clients support this.
func (h *Handler) handleStratumNonceSubscribe(m StratumRequestMsg) {
	h.p.log.Debugln("ID = "+strconv.Itoa(m.ID)+", Method = "+m.Method+", params = ", m.Params)

	r := StratumResponseMsg{ID: m.ID}
	r.Result = json.RawMessage(`true`)
	r.Error = json.RawMessage(`null`)

	h.sendResponse(r)

}

func (h *Handler) handleStratumSubmit(m StratumRequestMsg) {
	var p [5]string
	// fmt.Printf("%s: %s Handle submit\n", time.Now(), h.s.printID())
	r := StratumResponseMsg{ID: m.ID}
	r.Result = json.RawMessage(`true`)
	r.Error = json.RawMessage(`null`)
	err := json.Unmarshal(m.Params, &p)
	if err != nil {
		h.log.Printf("Unable to parse mining.submit params: %v\n", err)
		r.Result = json.RawMessage(`false`)
		r.Error = json.RawMessage(`["20","Parse Error"]`)
	}
	name := p[0]
	var jobID uint64
	fmt.Sscanf(p[1], "%x", &jobID)
	extraNonce2 := p[2]
	nTime := p[3]
	nonce := p[4]
	//	h.s.CurrentWorker.SetLastShareTime(time.Now())
	if h.s.CurrentWorker.checkDiffOnNewShare() {
		h.sendSetDifficulty(h.s.CurrentWorker.CurrentDifficulty())
	}

	h.log.Debugln("name = " + name + ", jobID = " + fmt.Sprintf("%X", jobID) + ", extraNonce2 = " + extraNonce2 + ", nTime = " + nTime + ", nonce = " + nonce)

	if h.s.CurrentJob == nil || // job just finished
		h.s.CurrentJob.JobID != jobID || // job is old/stale
		h.p.sourceBlock.MerkleRoot() != h.s.CurrentJob.MerkleRoot { //block changed since job started
		if h.s.CurrentJob == nil {
			r.Error = json.RawMessage(`["21","Stale - Job just finished"]`)
			h.s.CurrentWorker.log.Printf("Stale Share rejected - Job just finished\n")
		} else if h.s.CurrentJob.JobID != jobID {
			r.Error = json.RawMessage(`["21","Stale - old/unknown job"]`)
			h.s.CurrentWorker.log.Printf("Stale Share rejected - old/unknown job\n")
		} else if h.p.sourceBlock.MerkleRoot() != h.s.CurrentJob.MerkleRoot {
			r.Error = json.RawMessage(`["21","Stale - block changed"]`)
			h.s.CurrentWorker.log.Printf("Stale Share rejected - block changed\n")
		}
		r.Result = json.RawMessage(`false`)
		h.s.CurrentWorker.IncrementInvalidSharesThisSessin()
		h.s.CurrentWorker.IncrementInvalidSharesThisBlock()
		h.s.CurrentWorker.IncrementStaleSharesThisSession()
		h.s.CurrentWorker.IncrementStaleSharesThisBlock()
		h.s.CurrentWorker.IncrementContinuousStaleCount()
		h.sendResponse(r)
		// seems like we have a race where the miner gets stuck on a stale job
		if h.s.CurrentWorker.ContinuousStaleCount() > 2 {
			h.sendStratumNotify()
		}
		// fmt.Printf("%s: %s Handle submit - done - stale\n", time.Now(), h.s.printID())

		return
	}
	h.s.CurrentWorker.log.Printf("Share Accepted\n")
	h.s.CurrentWorker.ClearContinuousStaleCount()
	h.s.CurrentWorker.IncrementSharesThisBlock()
	h.s.CurrentWorker.IncrementSharesThisSession()
	b := h.s.CurrentJob.Block // copying the block takes time - of course if it changes right away, this job becomes stale
	bhNonce, err := hex.DecodeString(nonce)
	for i := range b.Nonce { // there has to be a better way to do this in golang
		b.Nonce[i] = bhNonce[i]
	}
	tb, _ := hex.DecodeString(nTime)
	b.Timestamp = types.Timestamp(encoding.DecUint64(tb))

	cointxn := h.p.coinB1()
	ex1, _ := hex.DecodeString(h.s.printNonce())
	ex2, _ := hex.DecodeString(extraNonce2)
	cointxn.ArbitraryData[0] = append(cointxn.ArbitraryData[0], ex1...)
	cointxn.ArbitraryData[0] = append(cointxn.ArbitraryData[0], ex2...)

	b.Transactions = append(b.Transactions, []types.Transaction{cointxn}...)
	h.p.log.Debugf("Waiting to lock pool\n")
	h.p.mu.Lock()
	h.s.CurrentWorker.log.Printf("BH hash is %064v\n", b.ID())
	h.s.CurrentWorker.log.Printf("Target is  %064x\n", h.p.persist.Target.Int())
	blockHash := b.ID()
	if bytes.Compare(h.p.persist.Target[:], blockHash[:]) >= 0 {
		h.s.CurrentWorker.log.Printf("Block is less than target\n")
	}
	h.p.log.Debugf("Unlocking pool\n")
	h.p.mu.Unlock()
	err = h.p.managedSubmitBlock(b)
	if err != nil && err != modules.ErrBlockUnsolved {
		h.log.Printf("Failed to SubmitBlock(): %v\n", err)
		r.Result = json.RawMessage(`false`)
		r.Error = json.RawMessage(`["20","Other/Unknown"]`)
		h.s.CurrentWorker.IncrementInvalidSharesThisSessin()
		h.s.CurrentWorker.IncrementInvalidSharesThisBlock()
		h.sendResponse(r)
		// fmt.Printf("%s: %s Handle submit - done - unknown\n", time.Now(), h.s.printID())
		return
	}
	if err != modules.ErrBlockUnsolved {
		h.s.CurrentWorker.Parent().log.Printf("Yay!!! Solved a block!!\n")
		h.s.CurrentWorker.log.Printf("Yay!!! Solved a block!!\n")
		h.s.CurrentJob = nil
		h.s.CurrentWorker.ClearInvalidSharesThisBlock()
		h.s.CurrentWorker.ClearSharesThisBlock()
		h.s.CurrentWorker.ClearStaleSharesThisBlock()
		h.s.CurrentWorker.IncrementBlocksFound()
	}
	h.sendResponse(r)
	// fmt.Printf("%s: %s Handle submit - done -new block\n", time.Now(), h.s.printID())
}

func (h *Handler) sendSetDifficulty(d float64) {
	var r StratumRequestMsg
	r.Method = "mining.set_difficulty"
	r.ID = 1 // assuming this ID is the response to the original subscribe which appears to be a 1
	// tb := make([]byte, 8)
	// d64, _ := d.Uint64()
	// binary.LittleEndian.PutUint64(tb, d64)
	// diff := hex.EncodeToString(tb)
	r.Params = json.RawMessage(fmt.Sprintf("[%f]", d))
	h.sendRequest(r)
}

func (h *Handler) sendStratumNotify() {
	var r StratumRequestMsg
	r.Method = "mining.notify"
	// fmt.Printf("%s: %s Send notify\n", time.Now(), h.s.printID())
	r.ID = 1 // assuming this ID is the response to the original subscribe which appears to be a 1
	job, _ := newJob(h.p)
	h.s.addJob(job)
	jobid := h.s.CurrentJob.printID()
	h.p.log.Debugf("Waiting to lock pool\n")
	h.p.mu.Lock()
	h.s.CurrentJob.Block = *h.p.sourceBlock // make a copy of the block and hold it until the solution is submitted
	h.s.CurrentJob.MerkleRoot = h.p.sourceBlock.MerkleRoot()
	h.p.log.Debugf("Unlocking pool\n")
	h.p.mu.Unlock()
	// fmt.Printf("%s: %s Send notify - block copied\n", time.Now(), h.s.printID())
	mbranch := crypto.NewTree()
	var buf bytes.Buffer
	for _, payout := range job.Block.MinerPayouts {
		payout.MarshalSia(&buf)
		mbranch.Push(buf.Bytes())
		buf.Reset()
	}

	for _, txn := range job.Block.Transactions {
		txn.MarshalSia(&buf)
		mbranch.Push(buf.Bytes())
		buf.Reset()
	}
	//
	// This whole approach needs to be revisited.  I basically am cheating to look
	// inside the merkle tree struct to determine if the head is a leaf or not
	//
	type SubTree struct {
		next   *SubTree
		height int // Int is okay because a height over 300 is physically unachievable.
		sum    []byte
	}

	type Tree struct {
		head         *SubTree
		hash         hash.Hash
		currentIndex uint64
		proofIndex   uint64
		proofSet     [][]byte
		cachedTree   bool
	}
	tr := *(*Tree)(unsafe.Pointer(mbranch))

	var merkle []string
	//	h.log.Debugf("mBranch Hash %s\n", mbranch.Root().String())
	for st := tr.head; st != nil; st = st.next {
		//		h.log.Debugf("Height %d Hash %x\n", st.height, st.sum)
		merkle = append(merkle, hex.EncodeToString(st.sum))
	}
	mbj, _ := json.Marshal(merkle)
	//	h.log.Debugf("merkleBranch: %s\n", mbj)

	bpm, _ := job.Block.ParentID.MarshalJSON()

	version := ""

	nbits := "1a08645a"

	buf.Reset()
	encoding.WriteUint64(&buf, uint64(job.Block.Timestamp))
	ntime := hex.EncodeToString(buf.Bytes())

	cleanJobs := true
	raw := fmt.Sprintf(`[ "%s", %s, "%s", "%s", %s, "%s", "%s", "%s", %t ]`,
		jobid, string(bpm), h.p.coinB1Txn(), h.p.coinB2(), mbj, version, nbits, ntime, cleanJobs)
	r.Params = json.RawMessage(raw)
	// fmt.Printf("%s: %s Send notify - sending request\n", time.Now(), h.s.printID())
	h.sendRequest(r)
	// fmt.Printf("%s: %s Send notify - done\n", time.Now(), h.s.printID())
}

// Dispatcher contains a map of ip addresses to handlers
type Dispatcher struct {
	handlers map[string]*Handler
	ln       net.Listener
	mu       sync.RWMutex
	p        *Pool
	log      *persist.Logger
}

//AddHandler connects the incoming connection to the handler which will handle it
func (d *Dispatcher) AddHandler(conn net.Conn) {
	addr := conn.RemoteAddr().String()
	handler := &Handler{conn, make(chan bool, 2), make(chan bool, numPendingNotifies), d.p, d.log, nil}
	d.mu.Lock()
	d.handlers[addr] = handler
	d.mu.Unlock()

	handler.Listen()

	<-handler.closed // when connection closed, remove handler from handlers
	d.mu.Lock()
	delete(d.handlers, addr)
	d.mu.Unlock()
}

// ListenHandlers listens on a passed port and upon accepting the incoming connection, adds the handler to deal with it
func (d *Dispatcher) ListenHandlers(port string) {
	var err error
	d.ln, err = net.Listen("tcp", ":"+port)
	if err != nil {
		d.log.Println(err)
		return
	}

	defer d.ln.Close()
	err = d.p.tg.Add()
	if err != nil {
		// If this goroutine is not run before shutdown starts, this
		// codeblock is reachable.
		return
	}
	defer d.p.tg.Done()

	for {
		var conn net.Conn
		var err error
		select {
		case <-d.p.tg.StopChan():
			d.ln.Close()
			return
		default:
			conn, err = d.ln.Accept() // accept connection
			if err != nil {
				d.log.Println(err)
				continue
			}
		}

		tcpconn := conn.(*net.TCPConn)
		tcpconn.SetKeepAlive(true)
		tcpconn.SetKeepAlivePeriod(30 * time.Second)
		tcpconn.SetNoDelay(true)

		go d.AddHandler(conn)
	}
}

func (d *Dispatcher) NotifyClients() {
	d.log.Printf("Notifying %d clients\n", len(d.handlers))
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, h := range d.handlers {
		h.notify <- true
	}
}

// newStratumID returns a function pointer to a unique ID generator used
// for more the unique IDs within the Stratum protocol
func (p *Pool) newStratumID() (f func() uint64) {
	f = func() uint64 {
		p.log.Debugf("Waiting to lock pool\n")
		p.mu.Lock()
		defer func() {
			p.log.Debugf("Unlocking pool\n")
			p.mu.Unlock()
		}()
		atomic.AddUint64(&p.stratumID, 1)
		return p.stratumID
	}
	return
}

func sPrintID(id uint64) string {
	return fmt.Sprintf("%016x", id)
}

func (p *Pool) coinB1() types.Transaction {
	s := fmt.Sprintf("\000     \"%s\"     \000", p.InternalSettings().PoolName)
	if ((len(modules.PrefixNonSia[:]) + len(s)) % 2) != 0 {
		// odd length, add extra null
		s = s + "\000"
	}
	cb := make([]byte, len(modules.PrefixNonSia[:])+len(s)) // represents the bytes appended later
	n := copy(cb, modules.PrefixNonSia[:])
	copy(cb[n:], s)
	return types.Transaction{
		ArbitraryData: [][]byte{cb},
	}
}

func (p *Pool) coinB1Txn() string {
	coinbaseTxn := p.coinB1()
	buf := new(bytes.Buffer)
	coinbaseTxn.MarshalSiaNoSignatures(buf)
	b := buf.Bytes()
	binary.LittleEndian.PutUint64(b[72:87], binary.LittleEndian.Uint64(b[72:87])+8)
	return hex.EncodeToString(b)
}
func (p *Pool) coinB2() string {
	return "0000000000000000"
}
