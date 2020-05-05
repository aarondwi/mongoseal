package mgofencedlock

import (
	"context"
	"testing"
)

func TestNew(t *testing.T) {
	m, err := New("mongodb://mgo:mgo@localhost:27017/?writeConcern=majority&readConcern=majority", "mgo", "ownerID", 2000)
	if err != nil {
		t.Fatalf("Failed creating mongohandler: %v", err)
	}
	defer m.Close()

	err = m.client.Ping(context.TODO(), nil)
	if err != nil {
		t.Fatalf("Error creating connection and ping mongodb: %v", err)
	}
}
