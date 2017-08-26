package main

import (
	"fmt"
	"time"

	"github.com/NebulousLabs/Sia/api"

	"net/url"

	"github.com/spf13/cobra"
)

var (
	poolCmd = &cobra.Command{
		Use:   "pool",
		Short: "Perform pool actions",
		Long:  "Perform pool actions and view pool status.",
		Run:   wrap(poolcmd),
	}

	poolConfigCmd = &cobra.Command{
		Use:   "config [setting] [value]",
		Short: "Read/Modify pool settings",
		Long: `Read/Modify pool settings.

Available settings:
    name:               Name you select for your pool
    acceptingshares:    Is your pool accepting shares
    networkport:        Stratum port for your pool
    operatorpercentage: What percentage of the block reward goes to the pool operator
    operatorwallet:     Pool operator sia wallet address
 `,
		Run: wrap(poolconfigcmd),
	}

	poolStartCmd = &cobra.Command{
		Use:   "start",
		Short: "Start mining pool",
		Long:  "Start mining pool, if the pool is already running, this command does nothing",
		Run:   wrap(poolstartcmd),
	}

	poolStopCmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop mining pool",
		Long:  "Stop mining pool (this may take a few moments).",
		Run:   wrap(poolstopcmd),
	}

	poolClientsCmd = &cobra.Command{
		Use:   "clients",
		Short: "List clients",
		Long:  "List client overview, use pool client <clientname> for details",
		Run:   wrap(poolclientscmd),
	}

	poolClientCmd = &cobra.Command{
		Use:   "client <clientname>",
		Short: "Get Client details",
		Long:  "Get Client details by name",
		Run:   wrap(poolclientcmd),
	}
)

// poolstartcmd is the handler for the command `siac pool start`.
// Starts the mining pool.
func poolstartcmd() {
	err := get("/pool/start")
	if err != nil {
		die("Could not start mining pool:", err)
	}
	fmt.Println("Mining pool is now running.")
}

// poolcmd is the handler for the command `siac pool`.
// Prints the status of the pool.
func poolcmd() {
	status := new(api.PoolGET)
	err := getAPI("/pool", status)
	if err != nil {
		die("Could not get pool status:", err)
	}
	config := new(api.PoolConfig)
	err = getAPI("/pool/config", config)
	if err != nil {
		die("Could not get pool config:", err)
	}
	poolStr := "off"
	if status.PoolRunning {
		poolStr = "on"
	}
	fmt.Printf(`Pool status:
Mining Pool:   %s
Pool Hashrate: %v GH/s
Blocks Mined: %d

Pool config:
Pool Name:              %s
Pool Accepting Shares   %t
Pool Stratum Port       %d
Operator Percentage     %.02f %%
Operator Wallet:        %s`,
		poolStr, status.PoolHashrate/1000000000, status.BlocksMined,
		config.Name, config.AcceptingShares, config.NetworkPort, config.OperatorPercentage, config.OperatorWallet)
}

// poolstopcmd is the handler for the command `siac pool stop`.
// Stops the CPU miner.
func poolstopcmd() {
	err := get("/pool/stop")
	if err != nil {
		die("Could not stop pool:", err)
	}
	fmt.Println("Stopped mining pool.")
}

// poolconfigcmd is the handler for the command `siac pool config [parameter] [value]`
func poolconfigcmd(param, value string) {
	var err error
	switch param {
	case "operatorwallet":
	case "name":
	case "operatorpercentage":
	case "acceptingshares":
	case "networkport":
	default:
		die("Unknown pool config parameter: ", param)
	}
	err = post("/pool/config", param+"="+url.PathEscape(value))
	if err != nil {
		die("Could not update pool settings:", err)

	}
}

func poolclientscmd() {
	clients := new(api.PoolClientsInfo)
	err := getAPI("/pool/clients", clients)
	if err != nil {
		die("Could not get pool clients:", err)
	}
	fmt.Printf("Clients List:\n\n")
	fmt.Printf("Number of Clients: %d\nNumber of Workers: %d\n\n", clients.NumberOfClients, clients.NumberOfWorkers)
	fmt.Printf("                         Client Name                                         Blocks Mined\n")
	for _, c := range clients.Clients {
		fmt.Printf("% 76.76s %d\n", c.ClientName, c.BlocksMined)
		fmt.Printf("     Worker Name      Last Share Time\n")
		for _, w := range c.Workers {
			fmt.Printf(" % -16s     %v\n", w.WorkerName, shareTime(w.LastShareTime))
		}
	}
}

func poolclientcmd(name string) {
	client := new(api.PoolClientInfo)
	err := getAPI("/pool/client?name="+name, client)
	if err != nil {
		die("Could not get pool client:", err)
	}
	fmt.Printf("\nClient Name: % 76.76s\nBlocks Mined: %d\n\n", client.ClientName, client.BlocksMined)
	fmt.Printf("                    Per Current Block           Per Current Session\n")
	fmt.Printf("Worker Name         Shares    Stale   Invalid   Shares    Stale   Invalid     Blocks Found   Last Share Time\n")
	for _, w := range client.Workers {
		fmt.Printf("% -16s  % 8d % 8d  % 8d % 8d % 8d  % 8d         % 8d   %v\n",
			w.WorkerName, w.SharesThisBlock, w.StaleSharesThisBlock, w.InvalidSharesThisBlock,
			w.SharesThisSession, w.StaleSharesThisSession, w.InvalidSharesThisSession, w.BlocksFound,
			shareTime(w.LastShareTime))
	}

}

func shareTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	timeSince := time.Since(t)
	switch timeSince {
	default:
		return timeSince.String() + " ago"
	}
}
