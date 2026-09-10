package send

import (
	"errors"
	"log"
)

// Sender has no body of its own, so a body operation on it must be refused.
type Sender interface {
	Send(id string) error
}

// Client sends things.
type Client struct {
	Host string
}

// Send logs and fails.
func (c *Client) Send(id string) error {
	log.Printf("sending %v to %v", id, c.Host)
	// TODO: retry
	return errors.New("not implemented")
}

// Other has a method of the same name, which no body operation may touch.
type Other struct{}

// Send does nothing.
func (o Other) Send(id string) error {
	log.Printf("sending %v", id)
	return nil
}

// Deliver is a top-level function, and the only other user of log. It calls it
// twice, so a substitution's count can be seen to be matches and not
// declarations.
func Deliver(id string) error {
	log.Printf("delivering %v", id)
	log.Printf("delivered %v", id)
	return nil
}

func use() error {
	var s Sender = &Client{Host: "h"}
	if err := s.Send("1"); err != nil {
		return err
	}
	if err := (Other{}).Send("2"); err != nil {
		return err
	}
	return Deliver("3")
}
