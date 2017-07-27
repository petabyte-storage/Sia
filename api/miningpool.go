package api

import (
	"fmt"
	"net/http"

	"net/url"

	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/types"
	"github.com/julienschmidt/httprouter"
)

type (
	// PoolGET contains the stats that is returned after a GET request
	// to /pool.
	PoolGET struct {
		PoolRunning  bool `json:"poolrunning"`
		BlocksMined  int  `json:"blocksmined"`
		PoolHashrate int  `json:"cpuhashrate"`
	}
	// PoolConfigGET contains the parameters you can set to config your pool
	PoolConfigGET struct {
		AcceptingShares    bool             `json:"acceptingshares"`
		OperatorPercentage float32          `json:"operatorpercentage"`
		NetworkPort        uint16           `json:"networkport"`
		Name               string           `json:"name"`
		OperatorWallet     types.UnlockHash `json:"operatorwallet"`
	}
)

// poolHandler handles the API call that queries the pool's status.
func (api *API) poolHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	pg := PoolGET{
		PoolRunning:  api.pool.GetRunning(),
		BlocksMined:  0,
		PoolHashrate: 0,
	}
	WriteJSON(w, pg)
}

// poolConfigHandlerPOST handles POST request to the /pool API endpoint, which sets
// the internal settings of the pool.
func (api *API) poolConfigHandlerPOST(w http.ResponseWriter, req *http.Request, params httprouter.Params) {
	settings, err := api.parsePoolSettings(req)
	if err != nil {
		WriteError(w, Error{"error parsing pool settings: " + err.Error()}, http.StatusBadRequest)
		return
	}
	err = api.pool.SetInternalSettings(settings)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// poolConfigHandler handles the API call that queries the pool's status.
func (api *API) poolConfigHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	settings, err := api.parsePoolSettings(req)
	if err != nil {
		WriteError(w, Error{"error parsing pool settings: " + err.Error()}, http.StatusBadRequest)
		return
	}
	pg := PoolConfigGET{
		Name:               settings.PoolName,
		AcceptingShares:    settings.AcceptingShares,
		OperatorPercentage: settings.PoolOperatorPercentage,
		NetworkPort:        settings.PoolNetworkPort,
		OperatorWallet:     settings.PoolOperatorWallet,
	}
	WriteJSON(w, pg)
}

// parsePoolSettings a request's query strings and returns a
// modules.PoolInternalSettings configured with the request's query string
// parameters.
func (api *API) parsePoolSettings(req *http.Request) (modules.PoolInternalSettings, error) {
	settings := api.pool.InternalSettings()

	if req.FormValue("operatorwallet") != "" {
		var x types.UnlockHash
		x, err := scanAddress(req.FormValue("operatorwallet"))
		if err != nil {
			fmt.Println(err)
			return modules.PoolInternalSettings{}, nil
		}
		settings.PoolOperatorWallet = x
	}
	if req.FormValue("acceptingshares") != "" {
		var x bool
		_, err := fmt.Sscan(req.FormValue("acceptingshares"), &x)
		if err != nil {
			return modules.PoolInternalSettings{}, nil
		}
		settings.AcceptingShares = x
	}
	if req.FormValue("operatorpercentage") != "" {
		var x float32
		_, err := fmt.Sscan(req.FormValue("operatorpercentage"), &x)
		if err != nil {
			return modules.PoolInternalSettings{}, nil
		}
		settings.PoolOperatorPercentage = x

	}
	if req.FormValue("networkport") != "" {
		var x uint16
		_, err := fmt.Sscan(req.FormValue("networkport"), &x)
		if err != nil {
			return modules.PoolInternalSettings{}, nil
		}
		settings.PoolNetworkPort = x

	}
	if req.FormValue("name") != "" {
		var x string
		_, err := fmt.Sscan(req.FormValue("name"), &x)
		if err != nil {
			return modules.PoolInternalSettings{}, nil
		}
		settings.PoolName, err = url.PathUnescape(x)
		if err != nil {
			return modules.PoolInternalSettings{}, nil
		}

	}

	return settings, nil
}

// poolStartHandler handles the API call that starts the pool.
func (api *API) poolStartHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.pool.StartPool()
	WriteSuccess(w)
}

// poolStopHandler handles the API call to stop the pool.
func (api *API) poolStopHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.pool.StopPool()
	WriteSuccess(w)
}
