# TaxHawk Network — Firewall Context

## Panorama
- **Host**: 10.3.1.40 (lvpanorama.taxhawk.lv)
- **PAN-OS**: 11.1.13-h1
- All sites managed centrally. Changes go to Panorama first, then pushed to device groups.

---

## Sites, Device Groups, and Subnets

### PRV — Provo HQ (New Building)
- **Device Group**: PRVHQ
- **Template**: Both_prvHQ
- **Firewalls**: prvHQfw1 / prvHQfw2 (PA-1420 HA)
- **Subnet**: 10.0.x.x

| Zone | Subnet | Purpose |
|------|--------|---------|
| Cameras | 10.0.3.0/24 | Verkada cameras |
| Building | 10.0.5.0/24 | Building automation |
| Printers | 10.0.6.0/24 | HP printers |
| ConferenceAV | 10.0.7-8.0/24 | Conference AV |
| VMware | 10.0.9.0/24 | vSphere management |
| Servers | 10.0.10.0/24 | Servers |
| Domain | 10.0.11.0/24 | Active Directory |
| Customer Support | 10.0.99.0/24 | CS department |
| Users | 10.0.100.0/22 | General user data |
| Guest | 10.0.104.0/23 | Guest / 802.1X fallback |
| IoT | 10.0.106.0/23 | IoT devices |
| VPN | — | Nova VPN (GlobalProtect) remote users |
| MGMT | 10.0.1.0/24 | Switch/device management |
| Outside | 136.41.65.89, 204.225.31.74/30, 50.151.131.98/29 | ISPs |

### PRVold — Provo Legacy (Secure Printing)
- **Device Groups**: prvFWs, prvDFW
- **Template**: Both_prvFWs
- **Firewalls**: PRVfw1 / PRVfw2 (PA-3220 HA)
- **Subnet**: 10.2.x.x

### LV — Las Vegas DC (Production)
- **Device Group**: lvFWs
- **Template**: Both_lvFWs
- **Firewalls**: lvFW1 / lvFW2 (PA-5410 HA)
- **Subnet**: 10.3.x.x

| Zone | Subnet | Purpose |
|------|--------|---------|
| MGMT | 10.3.1.0/24 | Management (Panorama at .40) |
| OOB | 10.3.2.0/24 | Out-of-band / FW mgmt |
| Domain | 10.3.6.0/24 | Active Directory |
| VMware | 10.3.7.0/24 | vSphere management |
| Admin | 10.3.9.0/24 | Admin access |
| Logging | 10.3.10.0/24 | Log aggregation |
| DB | 10.3.50.0/23 | Production database |
| Tools | 10.3.58.0/24 | DevOps tools |
| App | 10.3.101.0/24 | Application servers |
| StageApp | 10.3.102.0/24 | Staging application |
| Ops | 10.3.103.0/24 | Operations |
| DMZ | 10.3.150.0/24 | DMZ |
| K8s | 10.3.160.0/23 | Kubernetes |
| PCIApp | 10.3.110.0/24 | PCI application servers |
| PCIdb | 10.3.111.0/24 | PCI databases |
| AWS Tunnel | — | LV ↔ AWS traffic (tunnel.22/23). Use this zone for all lvFWs rules targeting AWS destinations. |

### SLC — Salt Lake City DC (Test/Stage)
- **Device Group**: slcFWs
- **Template**: Both_slcFW
- **Firewalls**: slcFW1 / slcFW2 (PA-5410 HA)
- **Subnet**: 10.1.x.x
- **VPN zone**: Talon VPN remote users terminate here

### AWS
- **Device Groups**: use1-az1, use1-az6
- **Subnet**: 10.128.x.x
- **Firewalls**: PA-VM (standalone, 10.128.38.38 / 10.128.39.141)

---

## How to Determine Device Group for a Rule

Match the **source IP** to its site:
- 10.0.x.x → PRVHQ
- 10.2.x.x → prvFWs
- 10.3.x.x → lvFWs
- 10.1.x.x → slcFWs
- 10.128.x.x → use1-az1 or use1-az6

If source and destination are on **different sites**, the rule lives in **both** device groups (one each). If traffic crosses sites via VPN tunnel, the rule must exist on both ends.

If source is a user (`user:` field is populated, source address is blank), **do not assume local**. The user may connect via:
- **Talon VPN** → VPN zone on **slcFWs** — rule goes in slcFWs
- **Nova VPN (GlobalProtect)** → VPN zone on **PRVHQ** — rule goes in PRVHQ
- **Local office** → Users zone on **PRVHQ** (10.0.100.0/22) — rule goes in PRVHQ

Search existing rules for the username to determine their access method. Look for rules with the user in source-user fields or rule names containing the username (e.g. `VPN_AWS_<username>`).

---

## Rulebase Convention

- **FW-specific device groups** (prvFWs, slcFWs, lvFWs, PRVHQ, prvDFW, azfw1, use1-az1, use1-az6) → use **post** rulebase (`post-rules.md`)
- **Shared / overall device groups** (DataCenter, Shared) → use **pre** rulebase (`pre-rules.md`)

---

## Naming Conventions

**Address objects**: Use the hostname if known (e.g., `lvLogstash7`, `lvK8sGrafana`). For IPs without a hostname, use `site-subnet-purpose` (e.g., `lv-10.3.10.36`). For FQDNs, use the hostname portion (e.g., `fraud-th-redshift-workgroup`).

**Prefer address groups over subnets** (this is about the **source**): When a request describes the source as a group of hosts (e.g., "K8s", "App servers", "DB servers") *without* giving a CIDR, use the existing address group — not a subnet object. Grep `address-groups.md` in the relevant device group and `Shared` to find the right group. Only use a full subnet object if the request explicitly specifies the entire subnet needs access.

When new hosts join an existing role, add them to that role's address group with `edit_address_group` (`add_members`) rather than creating a parallel object set. Always use `add_members`/`remove_members`, never `static_members` — the latter replaces the whole membership list and can silently drop existing members.

**Do not apply this rule to the destination as a licence to narrow scope.** See "Scope fidelity" under Important Constraints — a stated destination CIDR is the approved scope, and an existing group is only an acceptable substitute if it *covers* that CIDR.

**Security rules**: Match existing patterns in the device group. Common patterns:
- VPN rules: `VPN AWS <resource>`, `VPN_AWS_<Username>`, `VPN <env> <resource>`
- General: `<requester-lastname>-<destination-purpose>-<port>` (e.g., `cluff-redshift-5439`)

**Application-first rule design**: Always prefer application-based matching over port-based service objects. Set `service=["any"]` and specify the application. Common mappings:
- TCP 443 → application `ssl`
- TCP 80 → application `web-browsing`
- TCP 22 → application `ssh`
- TCP 3306 → application `mysql`
- TCP 5432 → application `postgresql`
- TCP 53 / UDP 53 → application `dns`

Only fall back to service objects for non-standard ports with no matching PAN-OS application.

---

## GitLab Issue Fields

Incoming requests follow this template. Field semantics are authoritative — read them, don't infer:

```
Title: Firewall Access | Firewall Change Request for <VPN user needing access, or the submitter if not a VPN rule>
Source Address: <hostname/IP of the server — or the user — trying to connect>
User: <username of who needs access via the VPN — ONLY for VPN rules, otherwise blank>
Requested For: <filled only when requested on someone else's behalf>
Destination Address: <server they are trying to reach — hostname/IP>
Service/Port: <port/application, e.g. 443/SSL, 80/HTTP, 53>
Reason for Request: <concise business justification>
How long is this needed: <duration>
Submitted by: <the OAuth-authenticated submitter>
Approver: <manager name>
Result: <Approve / blank>
Comment: <optional>
Director Approver: <name>
Comment: <optional>
```

**`User:` pipeline caveat.** Some pipeline-created tickets (author `Gitlab`) wrongly stamp the submitter's SSO **email** into `User:` even for pure machine/host rules — a Power Automate mapping artifact (it-access-mcp#3), not a statement of rule scope. The upstream payload keeps these separate (`username` = VPN user, `email` = OAuth submitter). Therefore:

- Treat `User:` as a source-user ONLY if it is a plausible VPN username (e.g. `jdoe` / `taxhawk\jdoe`) AND the request is genuinely user-scoped.
- An email address in `User:` — especially one matching the submitter — is attribution, not scope. Ignore it for rule construction; never build a source-user match or infer a VPN type from it.
- `Requested For:` and `Submitted by:` are attribution fields, never rule scope.

Business approval (Result: Approve) means the requester's manager has already signed off. Your job is the technical implementation — not re-approving the business need.

---

## Important Constraints

- **Never** skip the approval request before making Panorama changes. Always post the change plan first and wait for operator approval.
- **Never** commit+push to production device groups (lvFWs, slcFWs) without explicit approval.
- **Always** check for an existing rule that already covers the traffic before creating a new one. A rule only "covers" a flow if **every** match condition passes — not just Source/Dest Address and Application. `Address = any` does not make a rule a wildcard: the **Source User** and **Category** (URL-category) columns independently gate it, and so do **Negate Source/Dest**, **Schedule**, and **Disabled**. Concluding "already allowed" after checking only zone/address/app is the exact mistake that closed #785 (missed Source User) and #788 (missed URL Category) incorrectly. When in doubt — especially when a non-empty Category whose membership you can't verify is the only thing that would make a rule match — treat the flow as NOT covered and plan the change.
- **Scope fidelity — never silently narrow or widen the approved scope.** The approver signed off on the scope written in the ticket. Two failure modes, both real:
  - **Narrowing (#826).** The destination read `10.3.50.0/23 (LV Production DB zone — lvScyllaDB1-3, lvAuth-RW, lvConfigLookup-R/RW, lvMefDB-query, lvDBCRS01/lvDBVCRS01)`. The approved destination is **the /23**. The parenthetical is an *illustrative* list, not an exhaustive one — the giveaway is the em-dash after a CIDR. The agent built a rule containing only those six host objects, so every shard VIP in 10.3.51.x was left out and the requester could not reach the shard DBs. Treat a parenthetical after a CIDR as examples unless it says "only" or "specifically".
  - **Widening (#827).** See the blast-radius constraint below.
- **When you do propose something narrower than the ticket, say so in a `### Scope Note` section of the plan** — state what the ticket asked for, what you are proposing, and why. A narrower plan may well be right (least privilege), but it must be a visible decision the operator can veto, never a silent substitution.
- **ALWAYS search for an existing rule to extend before creating a new one.** Search by application, service/port, and destination across the device group's pre + post rulebases and its parents (DataCenter, Shared); if a rule matches the intent, extend it — add the new user to Source User or the new server to the address list/group. A new rule is the last resort, allowed only when the plan lists the candidate rules considered and why each could not be extended: (a) a genuinely new application/destination with no precedent, or (b) every candidate is too broad / a different access level, so extending would over-grant. A named user gaining VPN access is not by itself a new-rule case — add them to the existing rule that grants that access. Host rebuilds, cluster refreshes, re-IPs, and server replacements should land as **edits** to existing rules or groups.
- **Classify replacement vs net-new before choosing a mechanism.** These get opposite defaults:
  - **Replacement / refresh** (rebuilt cluster, re-imaged server, re-IP, "same access as X") → the predecessor's existing rule set **is** the approved baseline. Enumerate every rule referencing the predecessor and default to **full parity**, group edit included. Reproducing already-approved access is parity, not scope widening. List every affected rule in the plan so the operator can veto individual ones.
  - **Net-new access** → default to editing the single specific rule that needs it, keeping blast radius at exactly one rule.
- **Blast radius — count references before editing any shared object, in both cases.** Grep every `pre-rules.md` and `post-rules.md` for the object's name, and also `address-groups.md` (a group nested in another group inherits the parent's references). **Count and list every referencing rule** and state the total in the plan. Adding a member to a shared group grants that member *everything* every referencing rule allows. Estimates are not good enough: on #827 the plan claimed `lvK8s_nodes` backed 6 rules; it actually backs **81** across DataCenter/lvFWs/slcFWs/prvFWs/Temp VPN — including outbound-Internet-any, AWS tunnel, and F5-management rules — and is nested inside the Shared group `Stage-Prod K8s nodes`. #827 was net-new (TCP/3306 to two hosts approved), so the single-rule edit was right. For a like-for-like rebuild of that same cluster, the group edit would have been right. A high reference count is a fact to report, not by itself a reason to narrow a replacement.
- If the source/destination description is vague (e.g., "Prod App servers"), use the subnet map to resolve the specific IPs/subnets before drafting.
- For user-based requests (User field populated **with a VPN username** — not a stamped email, see the `User:` pipeline caveat above — and no server Source Address), search existing rules for the user to determine their VPN type before assuming PRVHQ/Users.
