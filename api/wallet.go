package api

import (
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/NebulousLabs/Sia/types"
)

// WalletSiafundsBalance contains fields relating to the siafunds balance.
type WalletSiafundsBalance struct {
	SiafundBalance      types.Currency
	SiacoinClaimBalance types.Currency
}

// walletAddressHandler handles the API request for a new address.
func (srv *Server) walletAddressHandler(w http.ResponseWriter, req *http.Request) {
	coinAddress, _, err := srv.wallet.CoinAddress(true) // true indicates that the address should be visible to the user
	if err != nil {
		writeError(w, "Failed to get a coin address", http.StatusInternalServerError)
		return
	}

	// Since coinAddress is not a struct, we define one here so that writeJSON
	// writes an object instead of a bare value. In addition, we transmit the
	// coinAddress as a hex-encoded string rather than a byte array.
	writeJSON(w, struct{ Address types.UnlockHash }{coinAddress})
}

// walletSendHandler handles the API call to send coins to another address.
func (srv *Server) walletSendHandler(w http.ResponseWriter, req *http.Request) {
	// Scan the inputs.
	var amount types.Currency
	var dest types.UnlockHash
	if strings.ContainsAny(req.FormValue("amount"), "Ee") {
		// exponential format
		amountRat := new(big.Rat)
		_, err := fmt.Sscan(req.FormValue("amount"), amountRat)
		if err != nil {
			writeError(w, "Malformed amount", http.StatusBadRequest)
			return
		}
		amount = types.NewCurrency(new(big.Int).Div(amountRat.Num(), amountRat.Denom()))
	} else {
		// standard format
		_, err := fmt.Sscan(req.FormValue("amount"), &amount)
		if err != nil {
			writeError(w, "Malformed amount", http.StatusBadRequest)
			return
		}
	}

	// Parse the string into an address.
	var destAddressBytes []byte
	_, err := fmt.Sscanf(req.FormValue("destination"), "%x", &destAddressBytes)
	if err != nil {
		writeError(w, "Malformed coin address", http.StatusBadRequest)
		return
	}
	copy(dest[:], destAddressBytes)

	// Spend the coins.
	_, err = srv.wallet.SpendCoins(amount, dest)
	if err != nil {
		writeError(w, "Failed to create transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeSuccess(w)
}

// walletSiafundsBalanceHandler handles the API call querying the balance of
// siafunds.
func (srv *Server) walletSiafundsBalanceHandler(w http.ResponseWriter, req *http.Request) {
	var wsb WalletSiafundsBalance
	wsb.SiafundBalance, wsb.SiacoinClaimBalance = srv.wallet.SiafundBalance()
	writeJSON(w, wsb)
}

// walletSiafundsWatchsiagaddressHandler handles the API request to watch a
// siag address.
func (srv *Server) walletSiafundsWatchsiagaddressHandler(w http.ResponseWriter, req *http.Request) {
	err := srv.wallet.WatchSiagSiafundAddress(req.FormValue("keyfile"))
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeSuccess(w)
}

// walletStatusHandler handles the API call querying the status of the wallet.
func (srv *Server) walletStatusHandler(w http.ResponseWriter, req *http.Request) {
	writeJSON(w, srv.wallet.Info())
}
