# Task text, collected with HexStrike

Same three fields as [escopo-de-tasks.md](escopo-de-tasks.md). Paste **Name**, **Description**, and **Goal**. Replace `example.com`. The hexstrike server is enabled before Create.

Every Description names the `mcp__hexstrike__` tools for that phase. A missing name is looked up with `SearchExtraTools` and `query` `select:mcp__hexstrike__<tool>`. The result goes to `insert_assets` or `record_fact`.

## Passive recon

**Name**

```
example.com — passive recon
```

**Description**

```
Target: example.com.
Allowed: public sources only for example.com. Collect only with mcp__hexstrike__subfinder_scan, mcp__hexstrike__waybackurls_discovery, mcp__hexstrike__gau_discovery, and mcp__hexstrike__dnsenum_scan. If a name is not in the tool list, call SearchExtraTools with query select:mcp__hexstrike__<tool>. Record subdomains, historical URLs, DNS names, and technologies with insert_assets or record_fact. A long result is a count plus at most 15 examples.
Forbidden: any traffic to example.com, including port scans, directory brute force, vulnerability scans, login attempts, and exploitation. Do not collect with Bash, exec, container.exec, or an invented tool name.
```

**Goal**

```
example.com has a recorded set of subdomains, historical URLs, DNS names, and technologies collected from public sources.
```

## Active recon

**Name**

```
example.com — active recon
```

**Description**

```
Target: example.com.
Allowed: for names already recorded under example.com, check that the name resolves and answers on port 80 or 443, and only with mcp__hexstrike__httpx_probe. If the name is not in the tool list, call SearchExtraTools with query select:mcp__hexstrike__httpx_probe. Record each name as answering or not with insert_assets or record_fact.
Forbidden: broad port scans, directory brute force, vulnerability scans, credential guessing, and exploitation against example.com. Do not collect with Bash, exec, container.exec, or an invented tool name.
```

**Goal**

```
Each name already recorded under example.com is recorded as answering or not on port 80 or 443.
```

## Scanning

**Name**

```
example.com — scanning
```

**Description**

```
Target: example.com.
Allowed: discover open ports on example.com only with mcp__hexstrike__nmap_scan, and record each service with insert_assets or record_fact. If the name is not in the tool list, call SearchExtraTools with query select:mcp__hexstrike__nmap_scan. A long result is a count plus at most 15 examples.
Forbidden: mcp__hexstrike__nmap_advanced_scan, exploiting a service, guessing credentials, and directory brute force on example.com. Do not collect with Bash, exec, container.exec, or an invented tool name.
```

**Goal**

```
Open ports on example.com are recorded, each with its service.
```

## Enumeration

**Name**

```
example.com — enumeration
```

**Description**

```
Target: example.com.
Allowed: for ports already recorded on example.com, record protocol and banner with the matching mcp__hexstrike__ tool. Look the name up with SearchExtraTools and call that exact name. Store the result with insert_assets or record_fact.
Forbidden: a new port sweep, exploitation, and credential guessing on example.com. Do not collect with Bash, exec, container.exec, or an invented tool name.
```

**Goal**

```
Each port already recorded on example.com has its protocol and banner recorded.
```

## Fingerprint

**Name**

```
example.com — fingerprint
```

**Description**

```
Target: example.com.
Allowed: record product names observed on example.com using only mcp__hexstrike__detect_technologies_ai and mcp__hexstrike__wafw00f_scan. If a name is not in the tool list, call SearchExtraTools with query select:mcp__hexstrike__<tool>. Store the result with insert_assets or record_fact.
Forbidden: exploitation, credential guessing, and scanning ports that are not already recorded on example.com. Do not collect with Bash, exec, container.exec, or an invented tool name.
```

**Goal**

```
Product names observed on example.com are recorded.
```

## Assessment

**Name**

```
example.com — assessment
```

**Description**

```
Target: example.com.
Allowed: list candidate weaknesses on example.com and record whether each was confirmed or rejected. Collect with the mcp__hexstrike__ tool named for that candidate. If the name is not in the tool list, call SearchExtraTools with query select:mcp__hexstrike__<tool>. Store the evidence with insert_assets or record_fact, then stop.
Forbidden: damage, data theft, persistence, and any step after proof is recorded, on example.com. Do not collect with Bash, exec, container.exec, or an invented tool name.
```

**Goal**

```
Each candidate weakness on example.com is recorded as confirmed or rejected.
```
