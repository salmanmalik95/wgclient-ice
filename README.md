# wgclient-ice
# Example Config File


<pre>

{
    "PrivateKey": "SOM/BFJbdMpECPzitT7qet1ioQgVTr5IXLnBts9NxUA=",
    "PreSharedKey": "",
    "WgIface": "utun100",
    "WgPort": 51820,
    "SSHKey":  "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEILgEqgdCS8xx6zIfFr5HWadqa1/fAi8XnRGAKW04RznJ\n-----END PRIVATE KEY-----\n",
    "Peers": [{
        "wgPubKey": "3aVSqPYzS6xxJ2eALUT92/l4paId00ICTSekjrr/Uj0=",
        "allowedIps": ["100.64.0.2/32"]

    }],
    "PeerConfig": {
        "address": "100.64.0.1/32"
    },
    "Stuns": [{
          "uri": "stun:netbird.extremecloudztna.com:3478",
          "protocol": 0
    }],
   "Turns": [{
          "hostConfig": {"uri": "turn:netbird.extremecloudztna.com:3478", "protocol": 0},
          "user": "self",
          "password": "d9zRngJwvpQ1SFIjKRMIYYenLXAzilYjkj41aeDu33s"
    }],
   "SignalService": {
          "uri": "netbird.extremecloudztna.com:10000",
          "protocol": "http"
    }
}


</pre>