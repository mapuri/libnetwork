package remote

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"

	"github.com/docker/docker/pkg/plugins"
	"github.com/docker/libnetwork/datastore"
	"github.com/docker/libnetwork/driverapi"
	_ "github.com/docker/libnetwork/testutils"
	"github.com/docker/libnetwork/types"
)

func decodeToMap(r *http.Request) (res map[string]interface{}, err error) {
	err = json.NewDecoder(r.Body).Decode(&res)
	return
}

func handle(t *testing.T, mux *http.ServeMux, method string, h func(map[string]interface{}) interface{}) {
	mux.HandleFunc(fmt.Sprintf("/%s.%s", driverapi.NetworkPluginEndpointType, method), func(w http.ResponseWriter, r *http.Request) {
		ask, err := decodeToMap(r)
		if err != nil {
			t.Fatal(err)
		}
		answer := h(ask)
		err = json.NewEncoder(w).Encode(&answer)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func setupPlugin(t *testing.T, name string, mux *http.ServeMux) func() {
	if err := os.MkdirAll("/usr/share/docker/plugins", 0755); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("unix", fmt.Sprintf("/usr/share/docker/plugins/%s.sock", name))
	if err != nil {
		t.Fatal("Could not listen to the plugin socket")
	}

	mux.HandleFunc("/Plugin.Activate", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"Implements": ["%s"]}`, driverapi.NetworkPluginEndpointType)
	})

	go http.Serve(listener, mux)

	return func() {
		listener.Close()
		if err := os.RemoveAll("/usr/share/docker/plugins"); err != nil {
			t.Fatal(err)
		}
	}
}

type testEndpoint struct {
	t              *testing.T
	src            string
	dst            string
	address        string
	addressIPv6    string
	macAddress     string
	gateway        string
	gatewayIPv6    string
	resolvConfPath string
	hostsPath      string
	nextHop        string
	destination    string
	routeType      int
}

func (test *testEndpoint) Interface() driverapi.InterfaceInfo {
	return nil
}

func (test *testEndpoint) AddInterface(mac net.HardwareAddr, ipv4 net.IPNet, ipv6 net.IPNet) error {
	ip4, net4, _ := net.ParseCIDR(test.address)
	ip6, net6, _ := net.ParseCIDR(test.addressIPv6)
	if ip4 != nil {
		net4.IP = ip4
		if !types.CompareIPNet(net4, &ipv4) {
			test.t.Fatalf("Wrong address given %+v", ipv4)
		}
	}
	if ip6 != nil {
		net6.IP = ip6
		if !types.CompareIPNet(net6, &ipv6) {
			test.t.Fatalf("Wrong address (IPv6) given %+v", ipv6)
		}
	}
	if test.macAddress != "" && mac.String() != test.macAddress {
		test.t.Fatalf("Wrong MAC address given %v", mac)
	}
	return nil
}

func (test *testEndpoint) InterfaceName() driverapi.InterfaceNameInfo {
	return test
}

func compareIPs(t *testing.T, kind string, shouldBe string, supplied net.IP) {
	ip := net.ParseIP(shouldBe)
	if ip == nil {
		t.Fatalf(`Invalid IP to test against: "%s"`, shouldBe)
	}
	if !ip.Equal(supplied) {
		t.Fatalf(`%s IPs are not equal: expected "%s", got %v`, kind, shouldBe, supplied)
	}
}

func compareIPNets(t *testing.T, kind string, shouldBe string, supplied net.IPNet) {
	_, net, _ := net.ParseCIDR(shouldBe)
	if net == nil {
		t.Fatalf(`Invalid IP network to test against: "%s"`, shouldBe)
	}
	if !types.CompareIPNet(net, &supplied) {
		t.Fatalf(`%s IP networks are not equal: expected "%s", got %v`, kind, shouldBe, supplied)
	}
}

func (test *testEndpoint) SetGateway(ipv4 net.IP) error {
	compareIPs(test.t, "Gateway", test.gateway, ipv4)
	return nil
}

func (test *testEndpoint) SetGatewayIPv6(ipv6 net.IP) error {
	compareIPs(test.t, "GatewayIPv6", test.gatewayIPv6, ipv6)
	return nil
}

func (test *testEndpoint) SetNames(src string, dst string) error {
	if test.src != src {
		test.t.Fatalf(`Wrong SrcName; expected "%s", got "%s"`, test.src, src)
	}
	if test.dst != dst {
		test.t.Fatalf(`Wrong DstPrefix; expected "%s", got "%s"`, test.dst, dst)
	}
	return nil
}

func (test *testEndpoint) AddStaticRoute(destination *net.IPNet, routeType int, nextHop net.IP) error {
	compareIPNets(test.t, "Destination", test.destination, *destination)
	compareIPs(test.t, "NextHop", test.nextHop, nextHop)

	if test.routeType != routeType {
		test.t.Fatalf(`Wrong RouteType; expected "%d", got "%d"`, test.routeType, routeType)
	}

	return nil
}

func TestGetEmptyCapabilities(t *testing.T) {
	var plugin = "test-net-driver-empty-cap"

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	handle(t, mux, "GetCapabilities", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}

	d := newDriver(plugin, p.Client)
	if d.Type() != plugin {
		t.Fatal("Driver type does not match that given")
	}

	_, err = d.(*driver).getCapabilities()
	if err == nil {
		t.Fatal("There should be error reported when get empty capability")
	}
}

func TestGetExtraCapabilities(t *testing.T) {
	var plugin = "test-net-driver-extra-cap"

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	handle(t, mux, "GetCapabilities", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{
			"Scope": "local",
			"foo":   "bar",
		}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}

	d := newDriver(plugin, p.Client)
	if d.Type() != plugin {
		t.Fatal("Driver type does not match that given")
	}

	c, err := d.(*driver).getCapabilities()
	if err != nil {
		t.Fatal(err)
	} else if c.DataScope != datastore.LocalScope {
		t.Fatalf("get capability '%s', expecting 'local'", c.DataScope)
	}
}

func TestGetInvalidCapabilities(t *testing.T) {
	var plugin = "test-net-driver-invalid-cap"

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	handle(t, mux, "GetCapabilities", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{
			"Scope": "fake",
		}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}

	d := newDriver(plugin, p.Client)
	if d.Type() != plugin {
		t.Fatal("Driver type does not match that given")
	}

	_, err = d.(*driver).getCapabilities()
	if err == nil {
		t.Fatal("There should be error reported when get invalid capability")
	}
}

func TestRemoteDriver(t *testing.T) {
	var plugin = "test-net-driver"

	ep := &testEndpoint{
		t:              t,
		src:            "vethsrc",
		dst:            "vethdst",
		address:        "192.168.5.7/16",
		addressIPv6:    "2001:DB8::5:7/48",
		macAddress:     "7a:56:78:34:12:da",
		gateway:        "192.168.0.1",
		gatewayIPv6:    "2001:DB8::1",
		hostsPath:      "/here/comes/the/host/path",
		resolvConfPath: "/there/goes/the/resolv/conf",
		destination:    "10.0.0.0/8",
		nextHop:        "10.0.0.1",
		routeType:      1,
	}

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	var networkID string

	handle(t, mux, "GetCapabilities", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{
			"Scope": "global",
		}
	})
	handle(t, mux, "CreateNetwork", func(msg map[string]interface{}) interface{} {
		nid := msg["NetworkID"]
		var ok bool
		if networkID, ok = nid.(string); !ok {
			t.Fatal("RPC did not include network ID string")
		}
		return map[string]interface{}{}
	})
	handle(t, mux, "DeleteNetwork", func(msg map[string]interface{}) interface{} {
		if nid, ok := msg["NetworkID"]; !ok || nid != networkID {
			t.Fatal("Network ID missing or does not match that created")
		}
		return map[string]interface{}{}
	})
	handle(t, mux, "CreateEndpoint", func(msg map[string]interface{}) interface{} {
		iface := map[string]interface{}{
			"Address":     ep.address,
			"AddressIPv6": ep.addressIPv6,
			"MacAddress":  ep.macAddress,
		}
		return map[string]interface{}{
			"Interface": iface,
		}
	})
	handle(t, mux, "Join", func(msg map[string]interface{}) interface{} {
		options := msg["Options"].(map[string]interface{})
		foo, ok := options["foo"].(string)
		if !ok || foo != "fooValue" {
			t.Fatalf("Did not receive expected foo string in request options: %+v", msg)
		}
		return map[string]interface{}{
			"Gateway":        ep.gateway,
			"GatewayIPv6":    ep.gatewayIPv6,
			"HostsPath":      ep.hostsPath,
			"ResolvConfPath": ep.resolvConfPath,
			"InterfaceName": map[string]interface{}{
				"SrcName":   ep.src,
				"DstPrefix": ep.dst,
			},
			"StaticRoutes": []map[string]interface{}{
				map[string]interface{}{
					"Destination": ep.destination,
					"RouteType":   ep.routeType,
					"NextHop":     ep.nextHop,
				},
			},
		}
	})
	handle(t, mux, "Leave", func(msg map[string]interface{}) interface{} {
		return map[string]string{}
	})
	handle(t, mux, "DeleteEndpoint", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{}
	})
	handle(t, mux, "EndpointOperInfo", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{
			"Value": map[string]string{
				"Arbitrary": "key",
				"Value":     "pairs?",
			},
		}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}

	d := newDriver(plugin, p.Client)
	if d.Type() != plugin {
		t.Fatal("Driver type does not match that given")
	}

	c, err := d.(*driver).getCapabilities()
	if err != nil {
		t.Fatal(err)
	} else if c.DataScope != datastore.GlobalScope {
		t.Fatalf("get capability '%s', expecting 'global'", c.DataScope)
	}

	netID := "dummy-network"
	err = d.CreateNetwork(netID, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}

	endID := "dummy-endpoint"
	err = d.CreateEndpoint(netID, endID, ep, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}

	joinOpts := map[string]interface{}{"foo": "fooValue"}
	err = d.Join(netID, endID, "sandbox-key", ep, joinOpts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.EndpointOperInfo(netID, endID); err != nil {
		t.Fatal(err)
	}
	if err = d.Leave(netID, endID); err != nil {
		t.Fatal(err)
	}
	if err = d.DeleteEndpoint(netID, endID); err != nil {
		t.Fatal(err)
	}
	if err = d.DeleteNetwork(netID); err != nil {
		t.Fatal(err)
	}
}

type failEndpoint struct {
	t *testing.T
}

func (f *failEndpoint) Interfaces() []*driverapi.InterfaceInfo {
	f.t.Fatal("Unexpected call of Interfaces")
	return nil
}
func (f *failEndpoint) AddInterface(int, net.HardwareAddr, net.IPNet, net.IPNet) error {
	f.t.Fatal("Unexpected call of AddInterface")
	return nil
}

func TestDriverError(t *testing.T) {
	var plugin = "test-net-driver-error"

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	handle(t, mux, "CreateEndpoint", func(msg map[string]interface{}) interface{} {
		return map[string]interface{}{
			"Err": "this should get raised as an error",
		}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}

	driver := newDriver(plugin, p.Client)

	if err := driver.CreateEndpoint("dummy", "dummy", &testEndpoint{t: t}, map[string]interface{}{}); err == nil {
		t.Fatalf("Expected error from driver")
	}
}

func TestMissingValues(t *testing.T) {
	var plugin = "test-net-driver-missing"

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	ep := &testEndpoint{
		t: t,
	}

	handle(t, mux, "CreateEndpoint", func(msg map[string]interface{}) interface{} {
		iface := map[string]interface{}{
			"Address":     ep.address,
			"AddressIPv6": ep.addressIPv6,
			"MacAddress":  ep.macAddress,
		}
		return map[string]interface{}{
			"Interfaces": []interface{}{iface},
		}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}
	driver := newDriver(plugin, p.Client)

	if err := driver.CreateEndpoint("dummy", "dummy", ep, map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
}

type rollbackEndpoint struct {
}

func (r *rollbackEndpoint) Interface() driverapi.InterfaceInfo {
	return nil
}

func (r *rollbackEndpoint) AddInterface(_ net.HardwareAddr, _ net.IPNet, _ net.IPNet) error {
	return fmt.Errorf("fail this to trigger a rollback")
}

func TestRollback(t *testing.T) {
	var plugin = "test-net-driver-rollback"

	mux := http.NewServeMux()
	defer setupPlugin(t, plugin, mux)()

	rolledback := false

	handle(t, mux, "CreateEndpoint", func(msg map[string]interface{}) interface{} {
		iface := map[string]interface{}{
			"Address":     "192.168.4.5/16",
			"AddressIPv6": "",
			"MacAddress":  "7a:12:34:56:78:90",
		}
		return map[string]interface{}{
			"Interface": interface{}(iface),
		}
	})
	handle(t, mux, "DeleteEndpoint", func(msg map[string]interface{}) interface{} {
		rolledback = true
		return map[string]interface{}{}
	})

	p, err := plugins.Get(plugin, driverapi.NetworkPluginEndpointType)
	if err != nil {
		t.Fatal(err)
	}
	driver := newDriver(plugin, p.Client)

	ep := &rollbackEndpoint{}

	if err := driver.CreateEndpoint("dummy", "dummy", ep, map[string]interface{}{}); err == nil {
		t.Fatalf("Expected error from driver")
	}
	if !rolledback {
		t.Fatalf("Expected to have had DeleteEndpoint called")
	}
}
