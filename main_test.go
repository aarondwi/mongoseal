package mgofencedlock

import (
	"context"
	"log"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestNew(t *testing.T) {
	m, err := New("mongodb://mgo:mgo@localhost:27017/", "mgo", "ownerID", 2000)
	if err != nil {
		t.Fatalf("Failed creating mongohandler: %v", err)
	}
	defer m.Close()

	err = m.client.Ping(context.TODO(), nil)
	if err != nil {
		t.Fatalf("Error creating connection and ping mongodb: %v", err)
	}
}

func TestNewFailed(t *testing.T) {
	_, err := New("mongodb://notexist:notexist@localhost:27017/", "mgo", "ownerID", 2000)
	if err == nil {
		t.Fatalf("Creating connection should fail but it is not")
	}
}

func TestAcquireRefreshDelete(t *testing.T) {
	m, err := New("mongodb://mgo:mgo@localhost:27017/", "mgo", "ownerID", 2)
	if err != nil {
		t.Fatalf("Failed creating mongohandler: %v", err)
	}
	defer m.Close()

	mainKey := "mainKey"
	otherKey := "otherKey"

	endChan := make(chan bool)
	// goroutine one gonna get the main lock
	// and followed all success case
	// until main goroutine delete the key on-purpose
	// this goroutine won't delete the key by itself
	// assuming failed node
	go func(endChan chan<- bool) {
		mgolock, err := m.AcquireLock(mainKey)
		if err != nil {
			log.Fatalf("Failed Getting Lock when no lock exists: %v", err)
			t.Fail()
		}
		defer m.DeleteLock(mgolock)
		log.Printf("goroutines 1 hold the key: %s", mainKey)

		time.Sleep(1 * time.Second) // second 1
		if !mgolock.IsValid() {
			log.Fatalf("The lock acquired should still be valid but it is not: %v", err)
			t.Fail()
		}

		time.Sleep(2 * time.Second) // second 3
		if !mgolock.IsValid() {
			log.Fatal("Should have refreshed the lock, but it has not")
			log.Fatalf("The lock acquired should still be valid but it is not: %v", err)
			t.Fail()
		}

		// at 3.25 we delete the key
		// so at 3.8 should fail to re-obtain the lock
		// and at 4.1 it is no longer valid
		time.Sleep(1000 * time.Millisecond)
		if mgolock.IsValid() {
			log.Fatal("Should have not refreshed the lock, but it has")
			t.Fail()
		}
		endChan <- true
	}(endChan)

	// goroutine 2 simulates failure in getting the lock
	// because the duration is 2 second
	// and this goroutine just wait for 1 second before acquiring with same id
	go func() {
		time.Sleep(1 * time.Second)
		log.Print("Goroutine 2 starts running")
		_, err := m.AcquireLock(mainKey)
		if err == nil {
			log.Fatal("This call should fail but it is not")
			t.Fail()
		}
		log.Print("Goroutine 2 correctly did not accept a valid lock")
		return
	}()

	// goroutine 3 simulates successfully obtain a lock
	// with different id
	go func() {
		time.Sleep(1 * time.Second)
		log.Print("Goroutine 3 starts running")
		mgolock, err := m.AcquireLock(otherKey)
		if err != nil {
			log.Fatalf("Failed getting different key from those lock existing: %v", err)
			t.Fail()
		}
		if !mgolock.IsValid() {
			log.Fatal("Another key not exists should be valid but is not")
		}
		defer m.DeleteLock(mgolock)
		log.Printf("Goroutine 3 accepting a valid key: %s", otherKey)
		return
	}()

	time.Sleep(3250 * time.Millisecond)
	filter := bson.D{bson.E{Key: "Key", Value: mainKey}}
	_, err = m.lockColl.DeleteOne(m.ctx, filter)
	if err != nil {
		log.Fatalf("Failed simulating missing key: %v", err)
	}

	// wait for last test of goroutine 1
	<-endChan
}

func TestIncreaseVersion(t *testing.T) {
	m, err := New("mongodb://mgo:mgo@localhost:27017/?writeConcern=majority&readConcern=majority", "mgo", "ownerID", 2)
	if err != nil {
		t.Fatalf("Failed creating mongohandler: %v", err)
	}
	defer m.Close()

	versionUpgradeKey := "versionUpgradeKey"
	endChan := make(chan bool)

	go func(endChan chan bool) {
		// unit of resolution is second
		time.Sleep(3 * time.Second)
		log.Print("Goroutine starts running")
		mgolock, err := m.AcquireLock(versionUpgradeKey)
		if err != nil {
			log.Fatal("This call should not fail but it is")
			t.Fail()
		}
		defer m.DeleteLock(mgolock)

		if mgolock.Version != 2 {
			log.Fatalf("The lock should be at version 2 but it is not, it is %d", mgolock.Version)
			t.Fail()
		}
		endChan <- true
	}(endChan)

	docs := bson.D{
		bson.E{Key: "Key", Value: versionUpgradeKey},
		bson.E{Key: "Version", Value: 1},
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "last_seen", Value: time.Now().Unix()},
	}
	res, err := m.lockColl.InsertOne(m.ctx, docs)
	if err != nil {
		log.Fatalf("Failed simulating dummy version 1 key: %v", err)
	}
	log.Print(res)
	defer m.lockColl.DeleteOne(m.ctx,
		bson.D{bson.E{Key: "Key", Value: versionUpgradeKey}})
	<-endChan
}
