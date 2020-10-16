package mongoseal

import (
	"context"
	"log"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// MgoLock is the object users get
// after lock is acquired at mongodb
//
// Cannot pool this object,
// because this object, by definition, should live in 1 ctx (for refresh and delete),
// meaning need to hold the ref to ctx
type MgoLock struct {
	sync.Mutex
	Key        string
	Version    int
	isValid    bool
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// IsValid handle goroutine-safe checking of lock's validity
//
// should be checked before running your lock-protected code
func (m *MgoLock) IsValid() bool {
	m.Lock()
	defer m.Unlock()
	return m.isValid
}
func (m *MgoLock) updateValidity(status bool) {
	m.Lock()
	defer m.Unlock()
	m.isValid = status
}

// Mongoseal is the core object to create by user,
// returning a factory that creates the lock
type Mongoseal struct {
	client                       *mongo.Client
	lockColl                     *mongo.Collection
	ownerID                      string
	expiryTimeSecond             int64
	needRefresh                  bool
	remainingBeforeRefreshSecond int64
}

// NewMongoseal creates our new Mongoseal.
// It needs a context (gonna create one if nil passed),
// mongoClient pointer object, and list of options
func NewMongoseal(
	client *mongo.Client,
	dbname string,
	ownerID string,
	expiryTimeSecond int64,
	needRefresh bool,
	remainingBeforeRefreshSecond int64) (*Mongoseal, error) {

	if remainingBeforeRefreshSecond <= 0 {
		remainingBeforeRefreshSecond = 1
	}

	coll := client.Database(dbname).Collection("lock")
	return &Mongoseal{
		client:                       client,
		lockColl:                     coll,
		ownerID:                      ownerID,
		expiryTimeSecond:             expiryTimeSecond,
		needRefresh:                  needRefresh,
		remainingBeforeRefreshSecond: remainingBeforeRefreshSecond,
	}, nil
}

// Close the connection to mongo
//
// Also cancel the context, stopping all child locks
func (m *Mongoseal) Close(ctx context.Context) {
	if m.client != nil {
		m.client.Disconnect(ctx)
	}
}

// AcquireLock creates lock records on mongodb
// and fetch the record to return to users
//
// In the background, it also creates a goroutine which
// periodically refresh lock validity, until the lock is deleted,
// or the given ctx is Done
func (m *Mongoseal) AcquireLock(ctx context.Context, key string) (*MgoLock, error) {
	currentTime := time.Now().Unix()
	filter := bson.D{
		bson.E{Key: "Key", Value: key},
		bson.E{
			Key: "$or",
			Value: bson.A{
				bson.D{bson.E{Key: "last_seen", Value: nil}},
				bson.D{
					bson.E{
						Key: "last_seen",
						// resolution unit is second
						// reducing (NOT removing) the chance from ntp ~250ms uncertainty
						Value: bson.M{"$lt": currentTime - m.expiryTimeSecond}}},
			}},
	}

	update := bson.D{
		bson.E{Key: "$inc", Value: bson.M{"Version": 1}},
		bson.E{
			Key: "$set",
			Value: bson.D{
				bson.E{Key: "owner", Value: m.ownerID},
				bson.E{Key: "last_seen", Value: currentTime},
			}},
	}
	_, err := m.lockColl.UpdateOne(
		ctx, filter, update,
		options.Update().SetUpsert(true))

	if err != nil {
		log.Printf("Failed Upserting lock into mongo: %v", err)
		return nil, err
	}

	mgolock := &MgoLock{}
	ctx, cancelFunc := context.WithCancel(ctx)
	mgolock.ctx = ctx
	mgolock.cancelFunc = cancelFunc

	filter = bson.D{
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "Key", Value: key}}
	err = m.lockColl.FindOne(mgolock.ctx, filter).Decode(mgolock)
	if err != nil {
		log.Printf("Just written lock not found, with error: %v", err)
		return nil, err
	}
	mgolock.updateValidity(true)

	go m.refreshLock(mgolock)
	return mgolock, nil
}

// doRefreshLockOnMongo is a helper function
// because we need to call refresh on 2 separate places
func (m *Mongoseal) doRefreshLockOnMongo(mgolock *MgoLock) (*mongo.UpdateResult, error) {
	filter := bson.D{
		bson.E{Key: "owner", Value: m.ownerID},
		bson.E{Key: "Key", Value: mgolock.Key},
		bson.E{Key: "Version", Value: mgolock.Version}}
	update := bson.D{
		bson.E{
			Key:   "$inc",
			Value: bson.M{"last_seen": m.expiryTimeSecond},
		}}

	return m.lockColl.UpdateOne(mgolock.ctx, filter, update)
}

// refreshLock periodically refresh the lock, to keep it alive
// defined by `expiryTimeSecond` and `remainingBeforeRefreshSecond`
//
// the logic is that
// we want to wait until `remainingBeforeRefreshSecond` before running the first,
// then do again in loop after each `expiryTimeSecond`
//
// Can not return mgoLock to pool here
// because the user may be still has references to the object
func (m *Mongoseal) refreshLock(mgolock *MgoLock) {
	if !m.needRefresh {
		time.Sleep(
			time.Duration(m.expiryTimeSecond) *
				time.Second)
		mgolock.updateValidity(false)
		return
	}

	time.Sleep(time.Duration(m.expiryTimeSecond-m.remainingBeforeRefreshSecond) * time.Second)

	ticker := time.NewTicker(time.Duration(m.expiryTimeSecond) * time.Second)

	// need to exactly copy this
	// because we want it to run when the diff is reached
	select {
	case <-mgolock.ctx.Done():
		mgolock.updateValidity(false)
		return
	default:
		result, err := m.doRefreshLockOnMongo(mgolock)
		if err != nil ||
			(result.MatchedCount == 0 &&
				result.ModifiedCount == 0 &&
				result.UpsertedCount == 0) {
			time.Sleep(
				time.Duration(
					m.remainingBeforeRefreshSecond) *
					time.Second)
			mgolock.updateValidity(false)
			return
		}
	}

	for {
		select {
		case <-mgolock.ctx.Done():
			mgolock.updateValidity(false)
			return
		case <-ticker.C:
			result, err := m.doRefreshLockOnMongo(mgolock)
			if err != nil ||
				(result.MatchedCount == 0 &&
					result.ModifiedCount == 0 &&
					result.UpsertedCount == 0) {
				time.Sleep(
					time.Duration(
						m.remainingBeforeRefreshSecond) *
						time.Second)
				mgolock.updateValidity(false)
				return
			}
		}
	}
}

// DeleteLock removes the record lock from mongodb
//
// No need to handler error, as error may mean the lock has been taken by others
func (m *Mongoseal) DeleteLock(mgolock *MgoLock) {
	// separate this by itself
	// valid or not, we need to force the lock to be un-usable,
	// and for it to be `right now`
	mgolock.updateValidity(false)
	mgolock.cancelFunc()

	// separate this by itself
	// because if still valid, we may need to remove the lock record
	if mgolock.IsValid() {
		filter := bson.D{
			bson.E{Key: "owner", Value: m.ownerID},
			bson.E{Key: "Key", Value: mgolock.Key},
			bson.E{Key: "Version", Value: mgolock.Version}}
		m.lockColl.DeleteOne(mgolock.ctx, filter)
	}
}
