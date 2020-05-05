package mgofencedlock

import (
	"context"
	"errors"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Lock is the object users get
// after lock is acquired at mongodb
type Lock struct {
	key      string
	lastSeen time.Time
	version  int
	ctx      context.Context
}

// MgoFencedLock is the core object to create by user
type MgoFencedLock struct {
	client       *mongo.Client
	lockColl     *mongo.Collection
	ctx          context.Context
	cancelFunc   context.CancelFunc
	ownerID      string
	expiryTimeMs int16
}

// New creates our new MgoFencedLock
func New(
	connectionURL string,
	dbname string,
	ownerID string,
	expiryTimeMs int16) (*MgoFencedLock, error) {
	ctx, cancelFunc := context.WithCancel(context.Background())

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(connectionURL))
	if err != nil {
		log.Fatalf("Failed connecting to mongo: %v", err)
		cancelFunc()
		return nil, err
	}

	coll := client.Database(dbname).Collection("lock")
	return &MgoFencedLock{
		client:       client,
		lockColl:     coll,
		ctx:          ctx,
		cancelFunc:   cancelFunc,
		ownerID:      ownerID,
		expiryTimeMs: expiryTimeMs,
	}, nil
}

// Close the connection to mongo
func (m *MgoFencedLock) Close() {
	if m.client != nil {
		m.client.Disconnect(m.ctx)
	}
	m.cancelFunc()
}

// AcquireLock creates lock records on mongodb
// and fetch the record to return to users
func (m *MgoFencedLock) AcquireLock(key string) (*Lock, error) {
	filter := bson.D{
		bson.E{Key: "key", Value: key},
		bson.E{
			Key: "$or",
			Value: bson.A{
				bson.E{Key: "last_seen", Value: nil},
				bson.E{Key: "last_seen", Value: "new Date() - 10"},
			}},
	}
	update := bson.D{
		bson.E{Key: "$inc", Value: bson.E{Key: "version", Value: 1}},
		bson.E{
			Key: "$set",
			Value: bson.D{
				bson.E{Key: "owner", Value: m.ownerID},
				bson.E{Key: "last_seen", Value: "new Date()"},
			}},
	}
	upsert := true
	result, err := m.lockColl.UpdateOne(
		m.ctx,
		filter, update,
		&options.UpdateOptions{Upsert: &upsert})

	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	if result.MatchedCount == 0 &&
		result.ModifiedCount == 0 &&
		result.UpsertedCount == 0 {
		return nil, errors.New("All return codes are 0")
	}

	filter = bson.D{
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "key", Value: key}}
	var lock *Lock
	err = m.lockColl.FindOne(m.ctx, filter).Decode(&lock)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}

	return lock, nil
}

// RefreshLock updates the last_seen attribute in mongodb
// by default, it is already run periodically on its own goroutine
func (m *MgoFencedLock) RefreshLock(lock *Lock) error {
	return nil
}

// DeleteLock removes the record lock from mongodb
// Returns nothing, as error may mean the lock has been taken by others
func (m *MgoFencedLock) DeleteLock(lock *Lock) {
	filter := bson.D{
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "key", Value: lock.key}}
	m.lockColl.DeleteOne(m.ctx, filter)
}
