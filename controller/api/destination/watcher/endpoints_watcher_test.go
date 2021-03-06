package watcher

import (
	"fmt"
	"sort"
	"testing"

	"github.com/linkerd/linkerd2/controller/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	consts "github.com/linkerd/linkerd2/pkg/k8s"
	logging "github.com/sirupsen/logrus"
)

type bufferingEndpointListener struct {
	added             []string
	removed           []string
	noEndpointsCalled bool
	noEndpointsExists bool
}

func newBufferingEndpointListener() *bufferingEndpointListener {
	return &bufferingEndpointListener{
		added:   []string{},
		removed: []string{},
	}
}

func addressString(address Address) string {
	addressString := fmt.Sprintf("%s:%d", address.IP, address.Port)
	if address.Identity != "" {
		addressString = fmt.Sprintf("%s/%s", addressString, address.Identity)
	}
	if address.AuthorityOverride != "" {
		addressString = fmt.Sprintf("%s/%s", addressString, address.AuthorityOverride)
	}
	return addressString
}

func (bel *bufferingEndpointListener) Add(set AddressSet) {
	for _, address := range set.Addresses {
		bel.added = append(bel.added, addressString(address))
	}
}

func (bel *bufferingEndpointListener) Remove(set AddressSet) {
	for _, address := range set.Addresses {
		bel.removed = append(bel.removed, addressString(address))
	}
}

func (bel *bufferingEndpointListener) NoEndpoints(exists bool) {
	bel.noEndpointsCalled = true
	bel.noEndpointsExists = exists
}

type bufferingEndpointListenerWithResVersion struct {
	added   []string
	removed []string
}

func newBufferingEndpointListenerWithResVersion() *bufferingEndpointListenerWithResVersion {
	return &bufferingEndpointListenerWithResVersion{
		added:   []string{},
		removed: []string{},
	}
}

func addressStringWithResVerson(address Address) string {
	return fmt.Sprintf("%s:%d:%s", address.IP, address.Port, address.Pod.ResourceVersion)
}

func (bel *bufferingEndpointListenerWithResVersion) Add(set AddressSet) {
	for _, address := range set.Addresses {
		bel.added = append(bel.added, addressStringWithResVerson(address))
	}
}

func (bel *bufferingEndpointListenerWithResVersion) Remove(set AddressSet) {
	for _, address := range set.Addresses {
		bel.removed = append(bel.removed, addressStringWithResVerson(address))
	}
}

func (bel *bufferingEndpointListenerWithResVersion) NoEndpoints(exists bool) {}

func TestEndpointsWatcher(t *testing.T) {
	for _, tt := range []struct {
		serviceType                      string
		k8sConfigs                       []string
		id                               ServiceID
		hostname                         string
		port                             Port
		expectedAddresses                []string
		expectedNoEndpoints              bool
		expectedNoEndpointsServiceExists bool
		expectedError                    bool
	}{
		{
			serviceType: "local services",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1
  namespace: ns
subsets:
- addresses:
  - ip: 172.17.0.12
    targetRef:
      kind: Pod
      name: name1-1
      namespace: ns
  - ip: 172.17.0.19
    targetRef:
      kind: Pod
      name: name1-2
      namespace: ns
  - ip: 172.17.0.20
    targetRef:
      kind: Pod
      name: name1-3
      namespace: ns
  - ip: 172.17.0.21
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-1
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.12`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-2
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.19`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-3
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.20`,
			},
			id:   ServiceID{Name: "name1", Namespace: "ns"},
			port: 8989,
			expectedAddresses: []string{
				"172.17.0.12:8989",
				"172.17.0.19:8989",
				"172.17.0.20:8989",
				"172.17.0.21:8989",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
			expectedError:                    false,
		},
		{
			// Test for the issue described in linkerd/linkerd2#1405.
			serviceType: "local NodePort service with unnamed port",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1
  namespace: ns
spec:
  type: NodePort
  ports:
  - port: 8989
    targetPort: port1`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1
  namespace: ns
subsets:
- addresses:
  - ip: 10.233.66.239
    targetRef:
      kind: Pod
      name: name1-f748fb6b4-hpwpw
      namespace: ns
  - ip: 10.233.88.244
    targetRef:
      kind: Pod
      name: name1-f748fb6b4-6vcmw
      namespace: ns
  ports:
  - port: 8990
    protocol: TCP`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-f748fb6b4-hpwpw
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  podIp: 10.233.66.239
  phase: Running`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-f748fb6b4-6vcmw
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  podIp: 10.233.88.244
  phase: Running`,
			},
			id:   ServiceID{Name: "name1", Namespace: "ns"},
			port: 8989,
			expectedAddresses: []string{
				"10.233.66.239:8990",
				"10.233.88.244:8990",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
			expectedError:                    false,
		},
		{
			// Test for the issue described in linkerd/linkerd2#1853.
			serviceType: "local service with named target port and differently-named service port",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: world
  namespace: ns
spec:
  type: ClusterIP
  ports:
    - name: app
      port: 7778
      targetPort: http`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: world
  namespace: ns
subsets:
- addresses:
  - ip: 10.1.30.135
    targetRef:
      kind: Pod
      name: world-575bf846b4-tp4hw
      namespace: ns
  ports:
  - name: app
    port: 7779
    protocol: TCP`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: world-575bf846b4-tp4hw
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  podIp: 10.1.30.135
  phase: Running`,
			},
			id:   ServiceID{Name: "world", Namespace: "ns"},
			port: 7778,
			expectedAddresses: []string{
				"10.1.30.135:7779",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
			expectedError:                    false,
		},
		{
			serviceType: "local services with missing addresses",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1
  namespace: ns
subsets:
- addresses:
  - ip: 172.17.0.23
    targetRef:
      kind: Pod
      name: name1-1
      namespace: ns
  - ip: 172.17.0.24
    targetRef:
      kind: Pod
      name: name1-2
      namespace: ns
  - ip: 172.17.0.25
    targetRef:
      kind: Pod
      name: name1-3
      namespace: ns
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-3
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.25`,
			},
			id:   ServiceID{Name: "name1", Namespace: "ns"},
			port: 8989,
			expectedAddresses: []string{
				"172.17.0.25:8989",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
			expectedError:                    false,
		},
		{
			serviceType: "local services with no endpoints",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name2
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 7979`,
			},
			id:                               ServiceID{Name: "name2", Namespace: "ns"},
			port:                             7979,
			expectedAddresses:                []string{},
			expectedNoEndpoints:              true,
			expectedNoEndpointsServiceExists: true,
			expectedError:                    false,
		},
		{
			serviceType: "external name services",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name3
  namespace: ns
spec:
  type: ExternalName
  externalName: foo`,
			},
			id:                               ServiceID{Name: "name3", Namespace: "ns"},
			port:                             6969,
			expectedAddresses:                []string{},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
			expectedError:                    true,
		},
		{
			serviceType:                      "services that do not yet exist",
			k8sConfigs:                       []string{},
			id:                               ServiceID{Name: "name4", Namespace: "ns"},
			port:                             5959,
			expectedAddresses:                []string{},
			expectedNoEndpoints:              true,
			expectedNoEndpointsServiceExists: false,
			expectedError:                    false,
		},
		{
			serviceType: "stateful sets",
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1
  namespace: ns
subsets:
- addresses:
  - ip: 172.17.0.12
    hostname: name1-1
    targetRef:
      kind: Pod
      name: name1-1
      namespace: ns
  - ip: 172.17.0.19
    hostname: name1-2
    targetRef:
      kind: Pod
      name: name1-2
      namespace: ns
  - ip: 172.17.0.20
    hostname: name1-3
    targetRef:
      kind: Pod
      name: name1-3
      namespace: ns
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-1
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.12`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-2
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.19`,
				`
apiVersion: v1
kind: Pod
metadata:
  name: name1-3
  namespace: ns
  ownerReferences:
  - kind: ReplicaSet
    name: rs-1
status:
  phase: Running
  podIP: 172.17.0.20`,
			},
			id:                               ServiceID{Name: "name1", Namespace: "ns"},
			hostname:                         "name1-3",
			port:                             5959,
			expectedAddresses:                []string{"172.17.0.20:5959"},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
		},
	} {
		tt := tt // pin
		t.Run("subscribes listener to "+tt.serviceType, func(t *testing.T) {
			k8sAPI, err := k8s.NewFakeAPI(tt.k8sConfigs...)
			if err != nil {
				t.Fatalf("NewFakeAPI returned an error: %s", err)
			}

			watcher := NewEndpointsWatcher(k8sAPI, logging.WithField("test", t.Name()))

			k8sAPI.Sync(nil)

			listener := newBufferingEndpointListener()

			err = watcher.Subscribe(tt.id, tt.port, tt.hostname, listener)
			if tt.expectedError && err == nil {
				t.Fatal("Expected error but was ok")
			}
			if !tt.expectedError && err != nil {
				t.Fatalf("Expected no error, got [%s]", err)
			}

			actualAddresses := make([]string, 0)
			actualAddresses = append(actualAddresses, listener.added...)
			sort.Strings(actualAddresses)

			testCompare(t, tt.expectedAddresses, actualAddresses)

			if listener.noEndpointsCalled != tt.expectedNoEndpoints {
				t.Fatalf("Expected noEndpointsCalled to be [%t], got [%t]",
					tt.expectedNoEndpoints, listener.noEndpointsCalled)
			}

			if listener.noEndpointsExists != tt.expectedNoEndpointsServiceExists {
				t.Fatalf("Expected noEndpointsExists to be [%t], got [%t]",
					tt.expectedNoEndpointsServiceExists, listener.noEndpointsExists)
			}
		})
	}
}

func TestEndpointsWatcherDeletion(t *testing.T) {
	k8sConfigs := []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
		`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1
  namespace: ns
subsets:
- addresses:
  - ip: 172.17.0.12
    targetRef:
      kind: Pod
      name: name1-1
      namespace: ns
  ports:
  - port: 8989`,
		`
apiVersion: v1
kind: Pod
metadata:
  name: name1-1
  namespace: ns
status:
  phase: Running
  podIP: 172.17.0.12`}

	for _, tt := range []struct {
		serviceType      string
		k8sConfigs       []string
		id               ServiceID
		hostname         string
		port             Port
		objectToDelete   interface{}
		deletingServices bool
	}{
		{
			serviceType:    "can delete endpoints",
			k8sConfigs:     k8sConfigs,
			id:             ServiceID{Name: "name1", Namespace: "ns"},
			port:           8989,
			hostname:       "name1-1",
			objectToDelete: &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "name1", Namespace: "ns"}},
		},
		{
			serviceType:    "can delete endpoints when wrapped in a DeletedFinalStateUnknown",
			k8sConfigs:     k8sConfigs,
			id:             ServiceID{Name: "name1", Namespace: "ns"},
			port:           8989,
			hostname:       "name1-1",
			objectToDelete: &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "name1", Namespace: "ns"}},
		},
		{
			serviceType:      "can delete services",
			k8sConfigs:       k8sConfigs,
			id:               ServiceID{Name: "name1", Namespace: "ns"},
			port:             8989,
			hostname:         "name1-1",
			objectToDelete:   &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "name1", Namespace: "ns"}},
			deletingServices: true,
		},
		{
			serviceType:      "can delete services when wrapped in a DeletedFinalStateUnknown",
			k8sConfigs:       k8sConfigs,
			id:               ServiceID{Name: "name1", Namespace: "ns"},
			port:             8989,
			hostname:         "name1-1",
			objectToDelete:   &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "name1", Namespace: "ns"}},
			deletingServices: true,
		},
	} {

		tt := tt // pin
		t.Run("subscribes listener to "+tt.serviceType, func(t *testing.T) {
			k8sAPI, err := k8s.NewFakeAPI(tt.k8sConfigs...)
			if err != nil {
				t.Fatalf("NewFakeAPI returned an error: %s", err)
			}

			watcher := NewEndpointsWatcher(k8sAPI, logging.WithField("test", t.Name()))

			k8sAPI.Sync(nil)

			listener := newBufferingEndpointListener()

			err = watcher.Subscribe(tt.id, tt.port, tt.hostname, listener)
			if err != nil {
				t.Fatal(err)
			}

			if tt.deletingServices {
				watcher.deleteService(tt.objectToDelete)

			} else {
				watcher.deleteEndpoints(tt.objectToDelete)
			}

			if !listener.noEndpointsCalled {
				t.Fatal("Expected NoEndpoints to be Called")
			}
		})

	}
}

func TestEndpointsWatcherServiceMirrors(t *testing.T) {
	for _, tt := range []struct {
		serviceType                      string
		k8sConfigs                       []string
		id                               ServiceID
		hostname                         string
		port                             Port
		expectedAddresses                []string
		expectedNoEndpoints              bool
		expectedNoEndpointsServiceExists bool
	}{
		{
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1-remote
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1-remote
  namespace: ns
  annotations:
    mirror.linkerd.io/remote-gateway-identity: "gateway-identity-1"
    mirror.linkerd.io/remote-svc-fq-name: "name1-remote-fq"
  labels:
    mirror.linkerd.io/mirrored-service: "true"
subsets:
- addresses:
  - ip: 172.17.0.12
  ports:
  - port: 8989`,
			},
			serviceType: "mirrored service with identity",
			id:          ServiceID{Name: "name1-remote", Namespace: "ns"},
			port:        8989,
			expectedAddresses: []string{
				"172.17.0.12:8989/gateway-identity-1/name1-remote-fq:8989",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
		},
		{
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1-remote
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1-remote
  namespace: ns
  annotations:
    mirror.linkerd.io/remote-svc-fq-name: "name1-remote-fq"
  labels:
    mirror.linkerd.io/mirrored-service: "true"
subsets:
- addresses:
  - ip: 172.17.0.12
  ports:
  - port: 8989`,
			},
			serviceType: "mirrored service without identity",
			id:          ServiceID{Name: "name1-remote", Namespace: "ns"},
			port:        8989,
			expectedAddresses: []string{
				"172.17.0.12:8989/name1-remote-fq:8989",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
		},

		{
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1-remote
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1-remote
  namespace: ns
  annotations:
    mirror.linkerd.io/remote-gateway-identity: "gateway-identity-1"
    mirror.linkerd.io/remote-svc-fq-name: "name1-remote-fq"
  labels:
    mirror.linkerd.io/mirrored-service: "true"
subsets:
- addresses:
  - ip: 172.17.0.12
  ports:
  - port: 9999`,
			},
			serviceType: "mirrored service with remapped port in endpoints",
			id:          ServiceID{Name: "name1-remote", Namespace: "ns"},
			port:        8989,
			expectedAddresses: []string{
				"172.17.0.12:9999/gateway-identity-1/name1-remote-fq:8989",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
		},
		{
			k8sConfigs: []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1-remote
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
				`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1-remote
  namespace: ns
  annotations:
    mirror.linkerd.io/remote-gateway-identity: ""
    mirror.linkerd.io/remote-svc-fq-name: "name1-remote-fq"
  labels:
    mirror.linkerd.io/mirrored-service: "true"
subsets:
- addresses:
  - ip: 172.17.0.12
  ports:
  - port: 9999`,
			},
			serviceType: "mirrored service with empty identity and remapped port in endpoints",
			id:          ServiceID{Name: "name1-remote", Namespace: "ns"},
			port:        8989,
			expectedAddresses: []string{
				"172.17.0.12:9999/name1-remote-fq:8989",
			},
			expectedNoEndpoints:              false,
			expectedNoEndpointsServiceExists: false,
		},
	} {
		tt := tt // pin
		t.Run("subscribes listener to "+tt.serviceType, func(t *testing.T) {
			k8sAPI, err := k8s.NewFakeAPI(tt.k8sConfigs...)
			if err != nil {
				t.Fatalf("NewFakeAPI returned an error: %s", err)
			}

			watcher := NewEndpointsWatcher(k8sAPI, logging.WithField("test", t.Name()))

			k8sAPI.Sync(nil)

			listener := newBufferingEndpointListener()

			err = watcher.Subscribe(tt.id, tt.port, tt.hostname, listener)

			if err != nil {
				t.Fatalf("NewFakeAPI returned an error: %s", err)
			}

			actualAddresses := make([]string, 0)
			actualAddresses = append(actualAddresses, listener.added...)
			sort.Strings(actualAddresses)

			testCompare(t, tt.expectedAddresses, actualAddresses)

			if listener.noEndpointsCalled != tt.expectedNoEndpoints {
				t.Fatalf("Expected noEndpointsCalled to be [%t], got [%t]",
					tt.expectedNoEndpoints, listener.noEndpointsCalled)
			}

			if listener.noEndpointsExists != tt.expectedNoEndpointsServiceExists {
				t.Fatalf("Expected noEndpointsExists to be [%t], got [%t]",
					tt.expectedNoEndpointsServiceExists, listener.noEndpointsExists)
			}
		})
	}
}

func testPod(resVersion string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			ResourceVersion: resVersion,
			Name:            "name1-1",
			Namespace:       "ns",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "172.17.0.12",
		},
	}
}

func endpoints(identity string) *corev1.Endpoints {
	return &corev1.Endpoints{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Endpoints",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "remote-service",
			Namespace: "ns",
			Annotations: map[string]string{
				consts.RemoteGatewayIdentity: identity,
				consts.RemoteServiceFqName:   "remote-service.svc.default.cluster.local",
			},
			Labels: map[string]string{
				consts.MirroredResourceLabel: "true",
			},
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP: "1.2.3.4",
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Port: 80,
					},
				},
			},
		},
	}
}

func TestEndpointsChangeDetection(t *testing.T) {

	k8sConfigs := []string{`
apiVersion: v1
kind: Service
metadata:
  name: remote-service
  namespace: ns
spec:
  ports:
  - port: 80
    targetPort: 80`,
		`
apiVersion: v1
kind: Endpoints
metadata:
  name: remote-service
  namespace: ns
  annotations:
    mirror.linkerd.io/remote-gateway-identity: "gateway-identity-1"
    mirror.linkerd.io/remote-svc-fq-name: "remote-service.svc.default.cluster.local"
  labels:
    mirror.linkerd.io/mirrored-service: "true"
subsets:
- addresses:
  - ip: 1.2.3.4
  ports:
  - port: 80`,
	}

	for _, tt := range []struct {
		serviceType       string
		id                ServiceID
		port              Port
		newEndpoints      *corev1.Endpoints
		expectedAddresses []string
	}{
		{
			serviceType:       "will update endpoints if identity is different",
			id:                ServiceID{Name: "remote-service", Namespace: "ns"},
			port:              80,
			newEndpoints:      endpoints("gateway-identity-2"),
			expectedAddresses: []string{"1.2.3.4:80/gateway-identity-1/remote-service.svc.default.cluster.local:80", "1.2.3.4:80/gateway-identity-2/remote-service.svc.default.cluster.local:80"},
		},

		{
			serviceType:       "will not update endpoints if identity is the same",
			id:                ServiceID{Name: "remote-service", Namespace: "ns"},
			port:              80,
			newEndpoints:      endpoints("gateway-identity-1"),
			expectedAddresses: []string{"1.2.3.4:80/gateway-identity-1/remote-service.svc.default.cluster.local:80"},
		},
	} {

		tt := tt // pin
		t.Run("subscribes listener to "+tt.serviceType, func(t *testing.T) {
			k8sAPI, err := k8s.NewFakeAPI(k8sConfigs...)
			if err != nil {
				t.Fatalf("NewFakeAPI returned an error: %s", err)
			}

			watcher := NewEndpointsWatcher(k8sAPI, logging.WithField("test", t.Name()))

			k8sAPI.Sync(nil)

			listener := newBufferingEndpointListener()

			err = watcher.Subscribe(tt.id, tt.port, "", listener)
			if err != nil {
				t.Fatal(err)
			}

			k8sAPI.Sync(nil)

			watcher.addEndpoints(tt.newEndpoints)

			actualAddresses := make([]string, 0)
			actualAddresses = append(actualAddresses, listener.added...)
			sort.Strings(actualAddresses)

			testCompare(t, tt.expectedAddresses, actualAddresses)
		})
	}
}

func TestPodChangeDetection(t *testing.T) {
	endpoints := &corev1.Endpoints{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Endpoints",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "name1",
			Namespace: "ns",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{
						IP:       "172.17.0.12",
						Hostname: "name1-1",
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: "ns",
							Name:      "name1-1",
						},
					},
				},
				Ports: []corev1.EndpointPort{
					{
						Port: 8989,
					},
				},
			},
		},
	}

	k8sConfigs := []string{`
apiVersion: v1
kind: Service
metadata:
  name: name1
  namespace: ns
spec:
  type: LoadBalancer
  ports:
  - port: 8989`,
		`
apiVersion: v1
kind: Endpoints
metadata:
  name: name1
  namespace: ns
subsets:
- addresses:
  - ip: 172.17.0.12
    hostname: name1-1
    targetRef:
      kind: Pod
      name: name1-1
      namespace: ns
  ports:
  - port: 8989`,
		`
apiVersion: v1
kind: Pod
metadata:
  name: name1-1
  namespace: ns
  resourceVersion: "1"
status:
  phase: Running
  podIP: 172.17.0.12`}

	for _, tt := range []struct {
		serviceType       string
		id                ServiceID
		hostname          string
		port              Port
		newPod            *corev1.Pod
		expectedAddresses []string
	}{
		{
			serviceType: "will update pod if resource version is different",
			id:          ServiceID{Name: "name1", Namespace: "ns"},
			port:        8989,
			hostname:    "name1-1",
			newPod:      testPod("2"),

			expectedAddresses: []string{"172.17.0.12:8989:1", "172.17.0.12:8989:2"},
		},
		{
			serviceType: "will not update pod if resource version is the same",
			id:          ServiceID{Name: "name1", Namespace: "ns"},
			port:        8989,
			hostname:    "name1-1",
			newPod:      testPod("1"),

			expectedAddresses: []string{"172.17.0.12:8989:1"},
		},
	} {
		tt := tt // pin
		t.Run("subscribes listener to "+tt.serviceType, func(t *testing.T) {
			k8sAPI, err := k8s.NewFakeAPI(k8sConfigs...)
			if err != nil {
				t.Fatalf("NewFakeAPI returned an error: %s", err)
			}

			watcher := NewEndpointsWatcher(k8sAPI, logging.WithField("test", t.Name()))

			k8sAPI.Sync(nil)

			listener := newBufferingEndpointListenerWithResVersion()

			err = watcher.Subscribe(tt.id, tt.port, tt.hostname, listener)
			if err != nil {
				t.Fatal(err)
			}

			err = k8sAPI.Pod().Informer().GetStore().Add(tt.newPod)
			if err != nil {
				t.Fatal(err)
			}
			k8sAPI.Sync(nil)

			watcher.addEndpoints(endpoints)

			actualAddresses := make([]string, 0)
			actualAddresses = append(actualAddresses, listener.added...)
			sort.Strings(actualAddresses)

			testCompare(t, tt.expectedAddresses, actualAddresses)
		})
	}
}
