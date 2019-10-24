package api

type ClusterParams struct {
	Namespace              string `json:"namespace"`
	ExternalAPIDNSName     string `json:"externalAPIDNSName"`
	ExternalAPIPort        uint   `json:"externalAPIPort"`
	ExternalAPIIPAddress   string `json:"externalAPIAddress"`
	ExternalOpenVPNDNSName string `json:"externalVPNDNSName"`
	ExternalOpenVPNPort    uint   `json:"externalVPNPort"`
	ServiceCIDR            string `json:"serviceCIDR"`
}
