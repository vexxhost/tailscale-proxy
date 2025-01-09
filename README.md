# `tailscale-proxy`

This is a small daemon which uses `tsnet` to create virtual devices in your
tailnet, which by default will proxy all TCP traffic into the target system
and any ports using `https` will be using the Tailscale issued certificate.

## Configuration

### Example

In the following example, we will configure the `tailscale-proxy` to proxy
all traffic from a device named `bmc-example-server` to the IP address
`1.2.3.4` and will also proxy the ports `443` and `5900` using a Tailscale
issued certificate.

The device will also be tagged with `bmc` and `example` tags.

```yaml
endpoints:
  - hostname: bmc-example-server
    ip: 1.2.3.4
    tags:
      - bmc
      - example
    listeners:
      - port: 443
        type: tls
      - port: 5900
        type: tls
      - port: 623
        type: udp
```

### Options

#### `endpoints`

In this section, you can configure all of the endpoints that will be proxied
by the `tailscale-proxy`.

##### `hostname`

This is the hostname that will be used to register the device with Tailscale.

##### `ip`

This is the IP address that the device will be proxied to.

##### `tags`

This is a list of tags that will be assigned to the device within Tailscale.

##### `listeners`

This is a list of ports that will be proxied by the `tailscale-proxy`, with
the type being any of the following:

- `tls`: This will proxy the port using a Tailscale issued certificate.
- `udp`: This will proxy UDP traffic to the target device.
