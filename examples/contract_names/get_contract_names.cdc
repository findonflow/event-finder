pub struct AccountInfo {
    pub(set) var address: Address
    pub(set) var contracts: {String:String}

    init(_ address: Address) {
        self.address = address
        self.contracts = {}
    }
}

pub fun main(addresses: [Address]): [AccountInfo] {
    let infos: [AccountInfo] = []
    for address in addresses {
        let account = getAccount(address)
        let contracts = account.contracts.names

        if contracts.length == 0 {
            continue
        }

        let contractMap : {String:String}= {}
        for c in contracts {
            if let dc=account.contracts.get(name:c) {
                let code =String.fromUTF8(dc.code) ?? "unparsable"
                contractMap[c]=code
            }else {
                contractMap[c]="not there"
            }
        }

        let info = AccountInfo(address)
        info.contracts = contractMap
        infos.append(info)
    }
    return infos
}
