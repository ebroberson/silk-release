package veth_test

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/cloudfoundry-incubator/silk/veth"
	"github.com/cloudfoundry-incubator/silk/veth/fakes"
	"github.com/containernetworking/cni/pkg/ns"
	"github.com/vishvananda/netlink"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Veth Manager", func() {
	var (
		containerNS ns.NetNS
		hostNS      ns.NetNS
		vethManager *veth.Manager
	)

	BeforeEach(func() {
		var err error
		containerNS, err = ns.NewNS()
		Expect(err).NotTo(HaveOccurred())
		hostNS, err = ns.NewNS()
		Expect(err).NotTo(HaveOccurred())

		vethManager = &veth.Manager{
			ContainerNSPath:  containerNS.Path(),
			HostNSPath:       hostNS.Path(),
			IPAdapter:        &veth.IPAdapter{},
			NamespaceAdapter: &veth.NamespaceAdapter{},
			NetlinkAdapter:   &veth.NetlinkAdapter{},
			SysctlAdapter:    &veth.SysctlAdapter{},
		}
	})

	AfterEach(func() {
		Expect(containerNS.Close()).To(Succeed())
	})

	Describe("Init", func() {
		It("initializes the manager", func() {
			err := vethManager.Init()
			Expect(err).NotTo(HaveOccurred())

			Expect(vethManager.HostNS.Path()).To(Equal(hostNS.Path()))
			Expect(vethManager.ContainerNS.Path()).To(Equal(containerNS.Path()))
		})

		Context("when getting the host namespace fails", func() {
			BeforeEach(func() {
				fakeNamespaceAdapter := &fakes.NamespaceAdapter{}
				fakeNamespaceAdapter.GetNSReturns(nil, errors.New("banana"))
				vethManager.NamespaceAdapter = fakeNamespaceAdapter
			})
			It("returns an error", func() {
				err := vethManager.Init()
				Expect(err).To(MatchError("Getting host namespace: banana"))
			})
		})

		Context("when getting the container namespace fails", func() {
			BeforeEach(func() {
				fakeNamespaceAdapter := &fakes.NamespaceAdapter{}
				fakeNamespaceAdapter.GetNSStub = func(path string) (ns.NetNS, error) {
					if path == hostNS.Path() {
						return hostNS, nil
					}
					if path == containerNS.Path() {
						return containerNS, nil
					}
					return nil, errors.New(path)
				}
				vethManager.NamespaceAdapter = fakeNamespaceAdapter
				vethManager.ContainerNSPath = "kiwi"
			})
			It("returns an error", func() {
				err := vethManager.Init()
				Expect(err).To(MatchError("Getting container namespace: kiwi"))
			})
		})
	})

	Describe("CreatePair", func() {
		BeforeEach(func() {
			err := vethManager.Init()
			Expect(err).NotTo(HaveOccurred())
		})
		It("creates a veth with one end in the targeted namespace", func() {
			_, err := vethManager.CreatePair("eth0", 1500)
			Expect(err).NotTo(HaveOccurred())

			err = containerNS.Do(func(_ ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName("eth0")
				Expect(err).NotTo(HaveOccurred())

				Expect(link.Attrs().Name).To(Equal("eth0"))

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns both the host and container link and namespaces", func() {
			vethPair, err := vethManager.CreatePair("eth0", 1500)
			Expect(err).NotTo(HaveOccurred())

			hardwareAddrRegex := `[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}:[0-9a-f]{2}`
			Expect(vethPair.Host.Link.Attrs().Name).To(MatchRegexp(`veth.*`))
			Expect(vethPair.Host.Link.Attrs().HardwareAddr).To(MatchRegexp(hardwareAddrRegex))
			Expect(vethPair.Container.Link.Attrs().Name).To(Equal("eth0"))
			Expect(vethPair.Host.Namespace).To(Equal(vethManager.HostNS))
			Expect(vethPair.Container.Namespace).To(Equal(vethManager.ContainerNS))
		})

		Context("when creating the veth pair fails", func() {
			BeforeEach(func() {
				fakeIPAdapter := &fakes.IPAdapter{}
				fakeIPAdapter.SetupVethReturns(nil, nil, errors.New("kiwi"))
				vethManager.IPAdapter = fakeIPAdapter
			})

			It("returns an error", func() {
				_, err := vethManager.CreatePair("eth0", 1500)
				Expect(err).To(MatchError("Setting up veth: kiwi"))
			})
		})
	})

	Describe("Destroy", func() {
		var vethName string
		BeforeEach(func() {
			err := vethManager.Init()
			Expect(err).NotTo(HaveOccurred())
			vethPair, err := vethManager.CreatePair("eth0", 1500)
			Expect(err).NotTo(HaveOccurred())
			vethName = vethPair.Container.Link.Attrs().Name
		})

		It("destroys the veth with the given name in the given namespace", func() {
			err := vethManager.Destroy(vethName)
			Expect(err).NotTo(HaveOccurred())

			err = containerNS.Do(func(_ ns.NetNS) error {
				defer GinkgoRecover()

				_, err = netlink.LinkByName(vethName)
				Expect(err).To(MatchError(ContainSubstring("not found")))

				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the interface doesn't exist", func() {
			BeforeEach(func() {
				fakeIPAdapter := &fakes.IPAdapter{}
				fakeIPAdapter.DelLinkByNameReturns(errors.New("kiwi"))
				vethManager.IPAdapter = fakeIPAdapter
			})
			It("returns an error", func() {
				err := vethManager.Destroy("ifname")
				Expect(err).To(MatchError("Deleting link: kiwi"))
			})
		})
	})

	Describe("AssignIP", func() {
		var vethPair *veth.Pair

		BeforeEach(func() {
			var err error
			err = vethManager.Init()
			Expect(err).NotTo(HaveOccurred())
			vethPair, err = vethManager.CreatePair("eth0", 1500)
			Expect(err).NotTo(HaveOccurred())

			err = vethManager.DisableIPv6(vethPair)
			Expect(err).NotTo(HaveOccurred())
		})

		It("sets point to point addresses in both host and container", func() {
			err := vethManager.AssignIP(vethPair, net.IPv4(10, 255, 4, 5))
			Expect(err).NotTo(HaveOccurred())

			err = vethPair.Host.Namespace.Do(func(_ ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(vethPair.Host.Link.Attrs().Name)
				Expect(err).NotTo(HaveOccurred())

				hostAddrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostAddrs).To(HaveLen(1))
				Expect(hostAddrs[0].IPNet.String()).To(Equal("169.254.0.1/32"))
				Expect(hostAddrs[0].Scope).To(Equal(int(netlink.SCOPE_LINK)))
				Expect(hostAddrs[0].Peer.String()).To(Equal("10.255.4.5/32"))
				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			err = vethPair.Container.Namespace.Do(func(_ ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(vethPair.Container.Link.Attrs().Name)
				Expect(err).NotTo(HaveOccurred())

				containerAddrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)

				Expect(err).NotTo(HaveOccurred())
				Expect(containerAddrs).To(HaveLen(1))
				Expect(containerAddrs[0].IPNet.String()).To(Equal("10.255.4.5/32"))
				Expect(containerAddrs[0].Scope).To(Equal(int(netlink.SCOPE_LINK)))
				Expect(containerAddrs[0].Peer.String()).To(Equal("169.254.0.1/32"))
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the address cannot be parsed", func() {
			BeforeEach(func() {
				fakeNetlink := &fakes.NetlinkAdapter{}
				fakeNetlink.ParseAddrReturns(nil, errors.New("kiwi"))
				vethManager.NetlinkAdapter = fakeNetlink
			})

			It("returns an error", func() {
				err := vethManager.AssignIP(vethPair, net.IPv4(10, 255, 4, 5))
				Expect(err).To(MatchError("parsing address 169.254.0.1/32: kiwi"))
			})
		})

		Context("when the device cannot be found", func() {
			BeforeEach(func() {
				fakeNetlink := &fakes.NetlinkAdapter{}
				fakeNetlink.LinkByNameReturns(nil, errors.New("kiwi"))
				fakeNetlink.ParseAddrReturns(&netlink.Addr{}, nil)
				vethManager.NetlinkAdapter = fakeNetlink
			})

			It("returns an error", func() {
				err := vethManager.AssignIP(vethPair, net.IPv4(10, 255, 4, 5))
				Expect(err).To(MatchError(fmt.Sprintf("find link by name %s: kiwi", vethPair.Host.Link.Attrs().Name)))
			})
		})

		Context("when the address cannot be added", func() {
			BeforeEach(func() {
				fakeNetlink := &fakes.NetlinkAdapter{}
				fakeNetlink.AddrAddReturns(errors.New("kiwi"))
				fakeNetlink.LinkByNameReturns(nil, nil)
				fakeNetlink.ParseAddrReturns(&netlink.Addr{}, nil)
				vethManager.NetlinkAdapter = fakeNetlink
			})

			It("returns an error", func() {
				err := vethManager.AssignIP(vethPair, net.IPv4(10, 255, 4, 5))
				Expect(err).To(MatchError("adding address 169.254.0.1/32: kiwi"))
			})
		})

		Context("when the container address cannot be parsed", func() {
			BeforeEach(func() {
				fakeNetlink := &fakes.NetlinkAdapter{}
				fakeNetlink.ParseAddrStub = func(addr string) (*netlink.Addr, error) {
					if addr == "10.255.4.5/32" {
						return nil, errors.New("kiwi")
					}

					return &netlink.Addr{}, nil
				}
				vethManager.NetlinkAdapter = fakeNetlink
			})

			It("returns an error", func() {
				err := vethManager.AssignIP(vethPair, net.IPv4(10, 255, 4, 5))
				Expect(err).To(MatchError("parsing address 10.255.4.5/32: kiwi"))
			})
		})
	})

	Describe("DisableIPv6", func() {
		var vethPair *veth.Pair

		BeforeEach(func() {
			var err error
			err = vethManager.Init()
			Expect(err).NotTo(HaveOccurred())
			vethPair, err = vethManager.CreatePair("eth0", 1500)
			Expect(err).NotTo(HaveOccurred())
		})

		It("removes all IPv6 addresses from the veth pair", func() {
			err := vethManager.DisableIPv6(vethPair)
			Expect(err).NotTo(HaveOccurred())

			err = vethPair.Host.Namespace.Do(func(_ ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(vethPair.Host.Link.Attrs().Name)
				Expect(err).NotTo(HaveOccurred())

				hostAddrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
				Expect(err).NotTo(HaveOccurred())
				Expect(hostAddrs).To(HaveLen(0))
				return nil
			})
			Expect(err).NotTo(HaveOccurred())

			err = vethPair.Container.Namespace.Do(func(_ ns.NetNS) error {
				defer GinkgoRecover()

				link, err := netlink.LinkByName(vethPair.Container.Link.Attrs().Name)
				Expect(err).NotTo(HaveOccurred())

				containerAddrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)

				Expect(err).NotTo(HaveOccurred())
				Expect(containerAddrs).To(HaveLen(0))
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when disabling ipv6 on the host interface fails", func() {
			BeforeEach(func() {
				fakeSysctlAdapter := &fakes.SysctlAdapter{}
				fakeSysctlAdapter.SysctlReturns("", errors.New("kiwi"))
				vethManager.SysctlAdapter = fakeSysctlAdapter
			})

			It("returns an error", func() {
				err := vethManager.DisableIPv6(vethPair)
				Expect(err).To(MatchError("Disabling IPv6 on host: kiwi"))
			})
		})

		Context("when disabling ipv6 in the container interface fails", func() {
			BeforeEach(func() {
				fakeSysctlAdapter := &fakes.SysctlAdapter{}
				fakeSysctlAdapter.SysctlStub = func(name string, params ...string) (string, error) {
					if strings.Contains(name, "eth0") {
						return "", errors.New("kiwi")
					}
					return "", nil
				}
				vethManager.SysctlAdapter = fakeSysctlAdapter
			})

			It("returns an error", func() {
				err := vethManager.DisableIPv6(vethPair)
				Expect(err).To(MatchError("Disabling IPv6 in container: kiwi"))
			})
		})
	})
})
