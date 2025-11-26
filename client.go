package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"

	auction "tacoterror/grpc"
)

// List cluster nodes here:
var clusterNodes = []string{
	":5001",
	":5002",
	":5003",
	":5004",
	":5005",
	":5006",
	":5007",
	":5008",
	":5009",
	// Add more if needed, otherwise switch to autoassignment
}

var rrCounter uint64 = 0

// pick node using round robin. If a node is no longer available, remove from cluster.
func pickNode() string {

	for {
		i := atomic.AddUint64(&rrCounter, 1)
		node := clusterNodes[(i-1)%uint64(len(clusterNodes))]

		conn, err := net.DialTimeout("tcp", node, 200*time.Millisecond)
		if err != nil {
			fmt.Println("Node unreachable, removing:", node)

			// remove dead node
			for idx, addr := range clusterNodes {
				if addr == node {
					clusterNodes = append(clusterNodes[:idx], clusterNodes[idx+1:]...)
					break
				}
			}

			if len(clusterNodes) == 0 {
				fmt.Println("No available nodes left! Exiting.")
				os.Exit(1)
			}

			continue // try next node
		}

		conn.Close()
		return node
	}
}

// start in terminal
func main() {
	reader := bufio.NewReader(os.Stdin)

	pruneDeadNodes()

	fmt.Println("=== Distributed Auction Client ===")
	fmt.Print("Enter your bidder name: ")
	bidderName, _ := reader.ReadString('\n')
	bidderName = strings.TrimSpace(bidderName)

	fmt.Print("Enter auction ID: ")
	aidStr, _ := reader.ReadString('\n')
	aidStr = strings.TrimSpace(aidStr)
	auctionID, _ := strconv.Atoi(aidStr)

	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  bid <amount>")
	fmt.Println("  result")
	fmt.Println("  exit")
	fmt.Println()

	for {
		fmt.Print("> ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		parts := strings.Split(line, " ")
		cmd := parts[0]

		switch cmd {

		case "exit":
			fmt.Println("Goodbye!")
			return

		case "bid":
			if len(parts) != 2 {
				fmt.Println("Usage: bid <amount>")
				continue
			}

			amount, err := strconv.Atoi(parts[1])
			if err != nil {
				fmt.Println("Invalid amount")
				continue
			}

			node := pickNode()
			fmt.Println("Selected node:", node)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			conn, err := grpc.DialContext(ctx, node, grpc.WithInsecure(), grpc.WithBlock())
			if err != nil {
				fmt.Println("Connection error:", err)
				cancel()
				continue
			}
			client := auction.NewAuctionServiceClient(conn)

			reply, err := client.Bid(ctx, &auction.BidRequest{
				BidderName: bidderName,
				AuctionId:  int64(auctionID),
				Amount:     int64(amount),
			})
			cancel()
			conn.Close()

			if err != nil {
				fmt.Println("Bid error:", err)
				continue
			}

			fmt.Printf("BidResult: status=%v highest=%d reason=%s\n",
				reply.BiddingStatus, reply.HighestBid, reply.Reason)

		case "result":
			node := pickNode()
			fmt.Println("Selected node:", node)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			conn, err := grpc.DialContext(ctx, node, grpc.WithInsecure(), grpc.WithBlock())
			if err != nil {
				fmt.Println("Connection error:", err)
				cancel()
				continue
			}
			client := auction.NewAuctionServiceClient(conn)

			reply, err := client.Result(ctx, &auction.ResultRequest{
				AuctionId: int64(auctionID),
			})
			cancel()
			conn.Close()

			if err != nil {
				fmt.Println("Result error:", err)
				continue
			}

			fmt.Printf("AuctionState: status=%v highest=%d winner=%s\n",
				reply.Status, reply.CurrentHighestBid, reply.AuctionWinner)

		default:
			fmt.Println("Unknown command. Available: bid, result, exit")
		}
	}
}

func pruneDeadNodes() {
	for i := 0; i < len(clusterNodes); {
		addr := clusterNodes[i]

		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			fmt.Println("Startup: removing unreachable node:", addr)
			clusterNodes = append(clusterNodes[:i], clusterNodes[i+1:]...)
			continue
		}

		conn.Close()
		i++
	}

	if len(clusterNodes) == 0 {
		fmt.Println("No reachable nodes at startup — exiting.")
		os.Exit(1)
	}
}
