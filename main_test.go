package mongoseal

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

func setupMongoForTest(ctx context.Context, connectionURL string) (*mongo.Client, error) {
	client, _ := mongo.Connect(
		ctx,
		options.Client().SetAppName("mongoseal"),
		options.Client().ApplyURI(connectionURL),
		options.Client().SetWriteConcern(writeconcern.New(writeconcern.WMajority())),
		options.Client().SetReadConcern(readconcern.Linearizable()))
	err := client.Ping(ctx, readpref.Nearest())
	if err != nil {
		return nil, err
	}

	return client, nil
}

func TestNew(t *testing.T) {
	ctx := context.Background()
	client, err := setupMongoForTest(ctx,
		"mongodb://mgo1:27017,mgo2:27018,mgo3:27019/mgo?replicaSet=rs")
	if err != nil {
		log.Printf("Failed connecting to mongo: %v", err)
	}
	m := New(client, "ownerID", Option{})
	if m.remainingBeforeRefreshSecond != 1 ||
		m.expiryTimeSecond != 5 ||
		m.needRefresh != false {
		t.Fatal("Failed setting up default value")
	}
	m.Close(ctx)
}

func TestAcquireRefreshDelete(t *testing.T) {
	ctx := context.Background()
	client, err := setupMongoForTest(ctx,
		"mongodb://mgo1:27017,mgo2:27018,mgo3:27019/mgo?replicaSet=rs")
	if err != nil {
		log.Printf("Failed connecting to mongo: %v", err)
	}
	m := New(client, "ownerID",
		Option{
			ExpiryTimeSecond:             3,
			NeedRefresh:                  true,
			RemainingBeforeRefreshSecond: 1,
		})
	defer m.Close(ctx)

	mainKey := "mainKey"
	otherKey := "otherKey"

	endChan := make(chan bool)
	// goroutine one gonna get the main lock
	// and followed all success case
	// until main goroutine delete the key on-purpose
	// this goroutine won't delete the key by itself
	// assuming failed node
	go func(endChan chan<- bool) {
		mgolock, err := m.AcquireLock(ctx, mainKey)
		if err != nil {
			log.Fatalf("Failed Getting Lock when no lock exists: %v", err)
		}
		log.Printf("goroutines 1 hold the key: %s", mainKey)

		time.Sleep(1 * time.Second) // second 1
		if !mgolock.IsValid() {
			log.Fatalf("The lock acquired should still be valid but it is not: %v", err)
		}

		// at 3.25 we delete the key.
		// but below should still be valid,
		// because it is refreshed at 2
		// and will be valid till 6
		time.Sleep(2500 * time.Millisecond) // second 3.5
		if !mgolock.IsValid() {
			log.Fatalf("Should have refreshed the lock and still be valid, but it has not, with error: %v", err)
		}

		// At 5 should fail to re-obtain the lock
		// but still valid at 5.05
		time.Sleep(1550 * time.Millisecond)
		if !mgolock.IsValid() {
			log.Fatal("Should still be valid, but it is not")
		}

		// and at 6.05 it is no longer valid
		time.Sleep(1 * time.Second)
		if mgolock.IsValid() {
			log.Fatal("Should have not refreshed the lock, but it has")
		}
		m.DeleteLock(mgolock)
		endChan <- true
	}(endChan)

	// goroutine 2 simulates failure in getting the lock
	// because the duration is 3 second
	// and this goroutine just wait for 1 second before acquiring with same id
	go func() {
		time.Sleep(1 * time.Second)
		log.Print("Goroutine 2 starts running")
		_, err := m.AcquireLock(ctx, mainKey)
		if err == nil {
			log.Fatal("This call should fail but it is not")
		}
		log.Print("Goroutine 2 correctly did not accept a valid lock")
		return
	}()

	// goroutine 3 simulates successfully obtain a lock with different id, and deleting it
	go func() {
		time.Sleep(1 * time.Second)
		log.Print("Goroutine 3 starts running")
		mgolock, err := m.AcquireLock(ctx, otherKey)
		if err != nil {
			log.Fatalf("Failed getting different key from those lock existing: %v", err)
		}
		if !mgolock.IsValid() {
			log.Fatal("Another key not exists should be valid but is not")
		}
		log.Printf("Goroutine 3 accepting a valid key: %s", otherKey)
		m.DeleteLock(mgolock)
		return
	}()

	time.Sleep(3250 * time.Millisecond)
	filter := bson.D{bson.E{Key: "Key", Value: mainKey}}
	_, err = m.lockColl.DeleteOne(ctx, filter)
	if err != nil {
		log.Fatalf("Failed simulating missing key: %v", err)
	}

	// wait for last test of goroutine 1
	<-endChan
}

func TestIncreaseVersionAndNotRefreshing(t *testing.T) {
	ctx := context.Background()
	client, err := setupMongoForTest(ctx,
		"mongodb://mgo1:27017,mgo2:27018,mgo3:27019/mgo?replicaSet=rs")
	if err != nil {
		log.Printf("Failed connecting to mongo: %v", err)
	}
	m := New(client, "ownerID",
		Option{
			ExpiryTimeSecond:             3,
			RemainingBeforeRefreshSecond: 1,
		})
	defer m.Close(ctx)

	versionUpgradeKey := "versionUpgradeKey"
	endChan := make(chan bool)

	go func(endChan chan bool) {
		time.Sleep(4000 * time.Millisecond)
		log.Print("Goroutine starts running")
		mgolock, err := m.AcquireLock(ctx, versionUpgradeKey)
		if err != nil {
			log.Fatal("This call should not fail but it is")
		}

		if mgolock.Version != 2 {
			log.Fatalf("The lock should be at version 2 but it is not, it is %d", mgolock.Version)
		}

		time.Sleep(3100 * time.Millisecond)
		if mgolock.IsValid() {
			log.Fatal("Should have not refreshed the lock, but it has")
		}
		m.DeleteLock(mgolock)
		endChan <- true
	}(endChan)

	docs := bson.D{
		bson.E{Key: "Key", Value: versionUpgradeKey},
		bson.E{Key: "Version", Value: 1},
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "last_seen", Value: time.Now().Unix()},
	}
	_, err = m.lockColl.InsertOne(ctx, docs)
	if err != nil {
		log.Fatalf("Failed simulating dummy version 1 key: %v", err)
	}
	defer m.lockColl.DeleteOne(ctx,
		bson.D{bson.E{Key: "Key", Value: versionUpgradeKey}})
	<-endChan
}

func BenchmarkAcquireReleaseLock(b *testing.B) {
	ctx := context.Background()
	client, err := setupMongoForTest(ctx,
		"mongodb://mgo1:27017,mgo2:27018,mgo3:27019/mongoseal?replicaSet=rs")
	if err != nil {
		log.Printf("Failed connecting to mongo: %v", err)
	}
	m := New(client, "ownerID", Option{ExpiryTimeSecond: 10})
	defer m.Close(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		keyName := fmt.Sprintf("key_%d", i+1)
		mgolock, err := m.AcquireLock(ctx, keyName)
		if err != nil {
			log.Fatalf("Failed Getting Lock when no lock supposed to be exist: %v", err)
		}
		m.DeleteLock(mgolock)
	}
	b.StopTimer()
}
