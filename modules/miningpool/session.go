package pool

import (
	"fmt"
)

//
// A Session captures the interaction with a miner client from whewn they connect until the connection is
// closed.  A session is tied to a single client and has many jobs associated with it
//
type Session struct {
	SessionID     uint64
	CurrentJob    *Job
	Client        *Client
	CurrentWorker *Worker
	ExtraNonce1   uint32
}

func newSession(p *Pool) (*Session, error) {
	id := p.newStratumID()
	s := &Session{
		SessionID:   id(),
		ExtraNonce1: uint32(id() & 0xffffffff),
	}
	return s, nil
}

func (s *Session) addClient(c *Client) {
	s.Client = c
}

func (s *Session) addJob(j *Job) {
	s.CurrentJob = j
}

func (s *Session) printID() string {
	return sPrintID(s.SessionID)
}

func (s *Session) printNonce() string {
	return fmt.Sprintf("%08x", s.ExtraNonce1)
}
