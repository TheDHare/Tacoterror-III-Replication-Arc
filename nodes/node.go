package nodes

import (
	"context"
	"sync"
	"time"

	auction "tacoterror/grpc"
)

var globalLeaderID int64 = 0
var globalMutex sync.Mutex

type AuctionState struct {
	AuctionID  int64
	HighestBid int64
	Winner     string
	Status     auction.AUCTION_STATUS // Maps to auction.AUCTION_STATUS
	EndTime    time.Time              // For determining an end to an auction
}

type Node struct {
	mu sync.Mutex

	NodeID          int64
	IsLeader        bool
	LeaderID        int64
	AuctionDuration time.Duration

	Auctions map[int64]*AuctionState
}

func NewNode(id int64, isLeader bool, duration time.Duration) *Node {
	n := &Node{
		NodeID:          id,
		IsLeader:        false, // will override below
		LeaderID:        0,
		AuctionDuration: duration,
		Auctions:        make(map[int64]*AuctionState),
	}

	globalMutex.Lock()
	defer globalMutex.Unlock()

	// If no leader assigned yet → self becomes leader
	if globalLeaderID == 0 {
		globalLeaderID = id
		n.IsLeader = true
		n.LeaderID = id
		return n
	}

	// If leader already exists → follow it
	n.IsLeader = false
	n.LeaderID = globalLeaderID
	return n
}

// For bidding
func (n *Node) HandleBid(ctx context.Context, req *auction.BidRequest) (*auction.BidReply, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	a, ok := n.Auctions[req.AuctionId]
	if !ok {
		// First time we see this auction id, create it
		a = &AuctionState{
			AuctionID:  req.AuctionId,
			HighestBid: 0,
			Winner:     "",
			Status:     auction.AUCTION_STATUS_ONGOING,
			EndTime:    time.Now().Add(n.AuctionDuration),
		}
		n.Auctions[req.AuctionId] = a
	}

	// check if auction has timed out
	if time.Now().After(a.EndTime) && a.Status == auction.AUCTION_STATUS_ONGOING {
		a.Status = auction.AUCTION_STATUS_FINISHED
	}

	// Reject bid if auction is finished
	if a.Status == auction.AUCTION_STATUS_FINISHED {
		return &auction.BidReply{
			BiddingStatus: auction.BIDDING_STATUS_FAIL,
			AuctionId:     req.AuctionId,
			HighestBid:    a.HighestBid,
			Reason:        "auction finished",
		}, nil
	}

	// Reject bid if it is lower than current highest bid
	if req.Amount <= a.HighestBid {
		return &auction.BidReply{
			BiddingStatus: auction.BIDDING_STATUS_FAIL,
			AuctionId:     req.AuctionId,
			HighestBid:    a.HighestBid,
			Reason:        "bid too low",
		}, nil
	}

	// Accept bid
	a.HighestBid = req.Amount
	a.Winner = req.BidderName

	return &auction.BidReply{
		BiddingStatus: auction.BIDDING_STATUS_SUCCESS,
		AuctionId:     req.AuctionId,
		HighestBid:    a.HighestBid,
		Reason:        "ok",
	}, nil
}

// Result
func (n *Node) HandleResult(ctx context.Context, req *auction.ResultRequest) (*auction.ResultReply, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	a, ok := n.Auctions[req.AuctionId]
	if !ok {
		// If we've never seen the auction id, treat as empty ongoing
		return &auction.ResultReply{
			Status:            auction.AUCTION_STATUS_ONGOING,
			AuctionId:         req.AuctionId,
			CurrentHighestBid: 0,
			AuctionWinner:     "",
		}, nil
	}

	// Auto-finish on read if time passed but no one has bid since
	if time.Now().After(a.EndTime) && a.Status == auction.AUCTION_STATUS_ONGOING {
		a.Status = auction.AUCTION_STATUS_FINISHED
	}

	// If the auction exists, return what is stored
	return &auction.ResultReply{
		Status:            auction.AUCTION_STATUS(a.Status),
		AuctionId:         a.AuctionID,
		CurrentHighestBid: a.HighestBid,
		AuctionWinner:     a.Winner,
	}, nil

}
