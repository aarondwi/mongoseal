db.getSiblingDB('mgo').lock.createIndex( { "Key": 1 }, { unique: true } )
db.getSiblingDB('mgo').lock.createIndex( { "last_seen": 1 }, { expireAfterSeconds: 600 } )