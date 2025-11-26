# Tacoterror-III-Replication-Arc
Repository for Distributed Systems Mandatory Assignment 5


how to run:

# Getting started
you need one terminal per node, so a minimum of 1 for the leader, and then additional once for every follower you wish to have.
When deciding on followers its important to distiquish between followers with the canBeLeader boolean and those without, when only those with can actually be promoted in case of leader failure. When your nodes are active and ready start a terminal for the client, and all further user input will happen in this terminal.

Below is the terminal commands needed for leader, backup followers aka those who can be promoted to leader, regular followers and the client + the avaieble commands in the client.

# Leader
1. Start the Leader: go run main.go --id=1 --addr=:5001 --replicas=:5002,:5003

# Backup follower (can inherit leadership)
2. backup leader/followers: go run main.go --id=2 --addr=:5002 --leader=:5001 --canBeLeader=true

# Regular follower
3. regular followers: go run main.go --id=3 --addr=:5003 --leader=:5001


# Client (round robin)
Start the interactive client:
go run client.go

The client picks a node automatically from :5001–:5009. more ports can be added in code.
Dead nodes are removed automatically.

Commands:
bid <amount>
result
exit

# Architecture Overview

## Node
Responsibilities:

Maintains local auction state

Processes client requests (Bid, Result)

Forwards bids to leader when follower

Applies replicated state updates from leader

Provides SyncState to followers on request

Triggers replication when leader accepts a new bid

Keeps sequence numbers to ensure ordering

Handles auction timeout and marks auction as finished


bid flow:
Follower: Bid → forwardBidToLeader → Leader
Leader:   Bid → update state → ReplicateAuctionState → Followers
Follower: Apply replication



## RM
Responsibilities:

Leader side:

Maintain follower addresses

Send replication RPCs on every accepted bid

Broadcast new leader after promotion


Follower side:

Store leader address

Forward bid requests to leader (via Node)

Sync full state on startup

Heartbeat-monitor leader

Promote to leader after repeated failures

Inform all nodes when becoming leader


Leader failure detection:

Every follower pings leader on a fixed interval

After repeated failures → promote self to leader

Broadcasts: NEW_LEADER:<address> to all peers



## AuctionServer
Responsibilities:

Expose Bid and Result RPCs

Expose ReplicateBid and SyncState internal RPCs

Call the corresponding Node handlers

The server contains no logic; it simply delegates to Node



## main.go
Responsibilities:

Parse flags to decide leader/follower role

Create Node and ReplicationManager

Assign followers or leader address

Enable leader monitoring (if configured)

Launch leader-change listener for promotions

Launch follower startup sync

Start gRPC server



# Communication flow

Bid request: Client → Any Node → Follower? → Forward to Leader → Apply → Replicate to Followers

Result: Client → Any Node → Return local (possibly replicated) state

Replication: Leader → ReplicateBid → Followers
             Followers → Apply state → Update Sequence

Inheritance: Follower monitors leader heartbeat
             Leader fails → follower promotes to leader → broadcasts NEW_LEADER
             Followers update leaderAddr → continue system
