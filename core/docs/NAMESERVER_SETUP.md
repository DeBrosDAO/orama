# Nameserver Setup Guide

This guide explains how to configure your domain registrar to use Orama Network nodes as authoritative nameservers.

## Overview

When you install Orama with the `--nameserver` flag, the node runs CoreDNS to serve DNS records for your domain. This enables:

- Dynamic DNS for deployments (e.g., `myapp.node-abc123.dbrs.space`)
- Wildcard DNS support for all subdomains
- ACME DNS-01 challenges for automatic SSL certificates

## Prerequisites

Before setting up nameservers, you need:

1. **Domain ownership** - A domain you control (e.g., `dbrs.space`)
2. **3+ VPS nodes** - Recommended for redundancy
3. **Static IP addresses** - Each VPS must have a static public IP
4. **Access to registrar DNS settings** - Admin access to your domain registrar

## Understanding DNS Records

### NS Records (Nameserver Records)
NS records tell the internet which servers are authoritative for your domain:
```
dbrs.space.  IN  NS  ns1.dbrs.space.
dbrs.space.  IN  NS  ns2.dbrs.space.
dbrs.space.  IN  NS  ns3.dbrs.space.
```

### Glue Records
Glue records are A records that provide IP addresses for nameservers that are under the same domain. They're required because:
- `ns1.dbrs.space` is under `dbrs.space`
- To resolve `ns1.dbrs.space`, you need to query `dbrs.space` nameservers
- But those nameservers ARE `ns1.dbrs.space` - circular dependency!
- Glue records break this cycle by providing IPs at the registry level

```
ns1.dbrs.space.  IN  A  141.227.165.168
ns2.dbrs.space.  IN  A  141.227.165.154
ns3.dbrs.space.  IN  A  141.227.156.51
```

## Installation

### Step 1: Install Orama on Each VPS

Install Orama with the `--nameserver` flag on each VPS that will serve as a nameserver:

```bash
# On VPS 1 (ns1)
sudo orama install \
  --nameserver \
  --domain dbrs.space \
  --vps-ip 141.227.165.168

# On VPS 2 (ns2)
sudo orama install \
  --nameserver \
  --domain dbrs.space \
  --vps-ip 141.227.165.154

# On VPS 3 (ns3)
sudo orama install \
  --nameserver \
  --domain dbrs.space \
  --vps-ip 141.227.156.51
```

### Step 2: Configure Your Registrar

#### For Namecheap

1. **Log into Namecheap Dashboard**
   - Go to https://www.namecheap.com
   - Navigate to **Domain List** → **Manage** (next to your domain)

2. **Add Glue Records (Personal DNS Servers)**
   - Go to **Advanced DNS** tab
   - Scroll down to **Personal DNS Servers** section
   - Click **Add Nameserver**
   - Add each nameserver with its IP:
     | Nameserver | IP Address |
     |------------|------------|
     | ns1.yourdomain.com | 141.227.165.168 |
     | ns2.yourdomain.com | 141.227.165.154 |
     | ns3.yourdomain.com | 141.227.156.51 |

3. **Set Custom Nameservers**
   - Go back to the **Domain** tab
   - Under **Nameservers**, select **Custom DNS**
   - Add your nameserver hostnames:
     - ns1.yourdomain.com
     - ns2.yourdomain.com
     - ns3.yourdomain.com
   - Click the green checkmark to save

4. **Wait for Propagation**
   - DNS changes can take 24-48 hours to propagate globally
   - Most changes are visible within 1-4 hours

#### For GoDaddy

1. Log into GoDaddy account
2. Go to **My Products** → **DNS** for your domain
3. Under **Nameservers**, click **Change**
4. Select **Enter my own nameservers**
5. Add your nameserver hostnames
6. For glue records, go to **DNS Management** → **Host Names**
7. Add A records for ns1, ns2, ns3

#### For Cloudflare (as Registrar)

1. Log into Cloudflare Dashboard
2. Go to **Domain Registration** → your domain
3. Under **Nameservers**, change to custom
4. Note: Cloudflare Registrar may require contacting support for glue records

#### For Google Domains

1. Log into Google Domains
2. Select your domain → **DNS**
3. Under **Name servers**, select **Use custom name servers**
4. Add your nameserver hostnames
5. For glue records, click **Add** under **Glue records**

## Verification

### Step 1: Verify NS Records

After propagation, check that NS records are visible:

```bash
# Check NS records from Google DNS
dig NS yourdomain.com @8.8.8.8

# Expected output should show:
# yourdomain.com.    IN  NS  ns1.yourdomain.com.
# yourdomain.com.    IN  NS  ns2.yourdomain.com.
# yourdomain.com.    IN  NS  ns3.yourdomain.com.
```

### Step 2: Verify Glue Records

Check that glue records resolve:

```bash
# Check glue records
dig A ns1.yourdomain.com @8.8.8.8
dig A ns2.yourdomain.com @8.8.8.8
dig A ns3.yourdomain.com @8.8.8.8

# Each should return the correct IP address
```

### Step 3: Test CoreDNS

Query your nameservers directly:

```bash
# Test a query against ns1
dig @ns1.yourdomain.com test.yourdomain.com

# Test wildcard resolution
dig @ns1.yourdomain.com myapp.node-abc123.yourdomain.com
```

### Step 4: Verify from Multiple Locations

Use online tools to verify global propagation:
- https://dnschecker.org
- https://www.whatsmydns.net

## Troubleshooting

### DNS Not Resolving

1. **Check CoreDNS is running:**
   ```bash
   sudo systemctl status coredns
   ```

2. **Check CoreDNS logs:**
   ```bash
   sudo journalctl -u coredns -f
   ```

3. **Verify port 53 is open:**
   ```bash
   sudo ufw status
   # Port 53 (TCP/UDP) should be allowed
   ```

4. **Test locally:**
   ```bash
   dig @localhost yourdomain.com
   ```

### Glue Records Not Propagating

- Glue records are stored at the registry level, not DNS level
- They can take longer to propagate (up to 48 hours)
- Verify at your registrar that they were saved correctly
- Some registrars require the domain to be using their nameservers first

### SERVFAIL Errors

Usually indicates CoreDNS configuration issues:

1. Check Corefile syntax
2. Verify RQLite connectivity
3. Check firewall rules

## Security Considerations

### Firewall Rules

Only expose necessary ports:

```bash
# Allow DNS from anywhere
sudo ufw allow 53/tcp
sudo ufw allow 53/udp

# Restrict admin ports to internal network
sudo ufw allow from 10.0.0.0/8 to any port 8080  # Health
sudo ufw allow from 10.0.0.0/8 to any port 9153  # Metrics
```

### Rate Limiting

Consider adding rate limiting to prevent DNS amplification attacks.
This can be configured in the CoreDNS Corefile.

## Multi-Node Coordination

When running multiple nameservers:

1. **All nodes share the same RQLite cluster** - DNS records are automatically synchronized
2. **Install in order** - First node bootstraps, others join
3. **Same domain configuration** - All nodes must use the same `--domain` value

## Related Documentation

- [CoreDNS RQLite Plugin](../pkg/coredns/README.md) - Technical details
- [Deployment Guide](./DEPLOYMENT_GUIDE.md) - Full deployment instructions
- [Architecture](./ARCHITECTURE.md) - System architecture overview
