# wgclient-ice
# Example Config File


<pre>

{
    "PrivateKey": "+GTl6rPRxzBQluVzL2dk9nHfiP51vUTv8bQHqmOAE0s=",
    "PreSharedKey": "",
    "WgIface": "wt0",
    "WgPort": 51820,
    "SSHKey":  "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEILgEqgdCS8xx6zIfFr5HWadqa1/fAi8XnRGAKW04RznJ\n-----END PRIVATE KEY-----\n",
    "Peers": [{
        "wgPubKey": "3aVSqPYzS6xxJ2eALUT92/l4paId00ICTSekjrr/Uj0=",
        "allowedIps": ["100.64.0.2/32"]

    }],
    "PeerConfig": {
        "address": "100.64.0.3/32"
    },
    "Stuns": [{
          "uri": "stun:turnserver1.extremecloudztna.com:3478",
          "protocol": 0
    }],
   "Turns": [{
          "hostConfig": {"uri": "turn:turnserver1.extremecloudztna.com:3478", "protocol": 0},
          "user": "salman",
          "password": "123"
    }],
   "SignalService": {
          "uri": "turnserver1.extremecloudztna.com:443",
          "protocol": "https"
    }
}


</pre>