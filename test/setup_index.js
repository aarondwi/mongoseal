db.getSiblingDB('mongoseal').lock.createIndex({ "Key": 1 }, { unique: true })
db.getSiblingDB('mongoseal').lock.createIndex({ "last_seen": 1 }, { expireAfterSeconds: 600 })