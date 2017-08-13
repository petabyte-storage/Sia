package pool

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log"
	"math/rand"
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
	p      *Pool
	s      *Session
}

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

	h.p.log.Println("New connection from " + h.conn.RemoteAddr().String())
	h.s, _ = newSession(h.p)
	h.p.log.Println("New sessioon: " + sPrintID(h.s.SessionID))
	dec := json.NewDecoder(h.conn)
	for {
		var m StratumRequestMsg
		select {
		case <-h.p.tg.StopChan():
			h.closed <- true // not closed until we return but we signal now so our parent knows
			return
		default:
			err := dec.Decode(&m)
			if err != nil {
				if err == io.EOF {
					h.p.log.Println("End connection")
				}
				h.closed <- true // send to dispatcher, that connection is closed
				return
			}
		}
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
		default:
			h.p.log.Debugln("Unknown stratum method: ", m.Method)
		}
	}
}

func (h *Handler) sendResponse(r StratumResponseMsg) {
	b, err := json.Marshal(r)
	if err != nil {
		h.p.log.Debugln("json marshal failed for id: ", r.ID, err)
	} else {
		_, err = h.conn.Write(b)
		if err != nil {
			h.p.log.Debugln("connection write failed for id: ", r.ID, err)
		}
		newline := []byte{'\n'}
		h.conn.Write(newline)
		h.p.log.Debugln(string(b))
	}
}
func (h *Handler) sendRequest(r StratumRequestMsg) {
	b, err := json.Marshal(r)
	if err != nil {
		h.p.log.Debugln("json marshal failed for id: ", r.ID, err)
	} else {
		_, err = h.conn.Write(b)
		if err != nil {
			h.p.log.Debugln("connection write failed for id: ", r.ID, err)
		}
		newline := []byte{'\n'}
		h.conn.Write(newline)
		h.p.log.Debugln(string(b))
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
			h.p.log.Printf("Unable to parse mining.authorize params: %v\n", err)
			r.Result = json.RawMessage(`false`)
		}
		client := p[0]
		worker := ""
		if strings.Contains(p[0], "./_") {
			s := strings.SplitN(p[0], "./_", 2)
			client = s[0]
			worker = s[1]
		}
		c := findClient(h.p, client)
		if c == nil {
			c, _ = newClient(h.p)
		}
		if c.Workers[worker] == nil {
			c.Workers[worker], _ = newWorker(c, worker)
		}
		h.p.log.Debugln("client = " + client + ", worker = " + worker)
		h.s.addClient(c)
		// TODO: figure out how to store this worker - probably in Session
	default:
		h.p.log.Debugln("Unknown stratum method: ", m.Method)
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
	r := StratumResponseMsg{ID: m.ID}
	r.Result = json.RawMessage(`true`)
	r.Error = json.RawMessage(`null`)
	err := json.Unmarshal(m.Params, &p)
	if err != nil {
		h.p.log.Printf("Unable to parse mining.submit params: %v\n", err)
		r.Result = json.RawMessage(`false`)
	}
	name := p[0]
	var jobID uint64
	fmt.Sscanf(p[1], "%x", &jobID)
	extraNonce2 := p[2]
	nTime := p[3]
	nonce := p[4]
	h.p.log.Debugln("name = " + name + ", jobID = " + fmt.Sprint(jobID) + ", extraNonce2 = " + extraNonce2 + ", nTime = " + nTime + ", nonce = " + nonce)
	if h.s.CurrentJob.JobID != jobID {
		r.Result = json.RawMessage(`false`)
		r.Error = json.RawMessage(`Stale`)
	}
	b := h.p.sourceBlock
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
	h.p.log.Debugf("BH hash is %064v\n", b.ID())
	h.p.log.Debugf("Target is  %064x\n", h.p.persist.Target.Int())
	err = h.p.managedSubmitBlock(*b)
	if err != nil {
		h.p.log.Printf("Failed to SubmitBlock(): %v\n", err)
		r.Result = json.RawMessage(`false`)
	}

	h.sendResponse(r)
	h.p.newSourceBlock()
	h.sendStratumNotify()
}
func (h *Handler) sendStratumNotify() {
	var r StratumRequestMsg
	r.Method = "mining.notify"
	r.ID = 1 // assuming this ID is the response to the original subscribe which appears to be a 1
	bh, _, err := h.p.HeaderForWork()
	if err != nil {
		h.p.log.Println("Error getting header for work: ", err)
		return
	}
	job, _ := newJob(h.p)
	h.s.addJob(job)
	jobid := job.printID()

	mbranch := crypto.NewTree()
	var buf bytes.Buffer
	for _, payout := range h.p.sourceBlock.MinerPayouts {
		payout.MarshalSia(&buf)
		mbranch.Push(buf.Bytes())
		buf.Reset()
	}

	for _, txn := range h.p.sourceBlock.Transactions {
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

	h.p.log.Debugf("mBranch Hash %s\n", mbranch.Root().String())
	for st := tr.head; st != nil; st = st.next {
		h.p.log.Debugf("Height %d Hash %x\n", st.height, st.sum)
	}
	var merkleBranch string
	if tr.head.height == 0 {
		// single leaf, so we need both this leaf and the following node in branch list
		merkleBranch = fmt.Sprintf(`["%s","%s"]`, hex.EncodeToString(tr.head.sum), hex.EncodeToString(tr.head.next.sum))
	} else {
		merkleBranch = fmt.Sprintf(`["%s"]`, hex.EncodeToString(tr.head.sum))
	}

	bpm, _ := bh.ParentID.MarshalJSON()

	version := ""

	nbits := "1a08645a"

	buf.Reset()
	encoding.WriteUint64(&buf, uint64(bh.Timestamp))
	ntime := hex.EncodeToString(buf.Bytes())

	cleanJobs := false
	raw := fmt.Sprintf(`[ "%s", %s, "%s", "%s", %s, "%s", "%s", "%s", %t ]`,
		jobid, string(bpm), h.p.coinB1Txn(), h.p.coinB2(), merkleBranch, version, nbits, ntime, cleanJobs)
	r.Params = json.RawMessage(raw)
	h.sendRequest(r)
}

// Dispatcher contains a map of ip addresses to handlers
type Dispatcher struct {
	handlers map[string]*Handler
	ln       net.Listener
	mu       sync.RWMutex
	p        *Pool
}

//AddHandler connects the incoming connection to the handler which will handle it
func (d *Dispatcher) AddHandler(conn net.Conn) {
	addr := conn.RemoteAddr().String()
	handler := &Handler{conn, make(chan bool, 1), d.p, nil}
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
		log.Println(err)
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
				//				log.Println(err)
				continue
			}
		}

		tcpconn := conn.(*net.TCPConn)
		tcpconn.SetKeepAlive(true)
		tcpconn.SetKeepAlivePeriod(10 * time.Second)

		go d.AddHandler(conn)
	}
}

// newStratumID returns a function pointer to a unique ID generator used
// for more the unique IDs within the Stratum protocol
func (p *Pool) newStratumID() (f func() uint64) {
	i := rand.Uint64()
	f = func() uint64 {
		atomic.AddUint64(&i, 1)
		return i
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
	// binary.LittleEndian.PutUint64(b[144:159], binary.LittleEndian.Uint64(b[144:159])+8)
	binary.LittleEndian.PutUint64(b[72:87], binary.LittleEndian.Uint64(b[72:87])+8)
	return hex.EncodeToString(b)
}
func (p *Pool) coinB2() string {
	return "0000000000000000"
}
func ConvertByte2Uint32(b []byte) []uint32 {
	if len(b)%4 != 0 {
		return nil
	}
	r := make([]uint32, len(b)/4)
	for i := range r {
		r[i] = *(*uint32)(unsafe.Pointer(&b[i*4]))
	}
	return r
}
