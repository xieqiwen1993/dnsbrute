package dns

import (
	"crypto/md5"
	"encoding/hex"

	"github.com/chuhades/dnsbrute/log"

	"github.com/miekg/dns"
)

var (
	authoritativeDNSServers = []string{}
	panAnalyticRecords      = map[string]uint32{}
	chPanAnalyticRecord     = make(chan DNSRecord)
)

type panAnalyticRecord struct {
	Domain string
	Ttl    uint32
	Type   string
	Target string
	IP     []string
}

func setAuthoritativeDNSServers() {
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(rootDomain), dns.TypeNS)
	in, err := dns.Exchange(msg, dnsServers[rand.Intn(len(dnsServers))])
	if err == nil && len(in.Answer) > 0 {
		for _, ans := range in.Answer {
			if ns, ok := ans.(*dns.NS); ok {
				authoritativeDNSServers = append(authoritativeDNSServers, TrimSuffixPoint(ns.Ns)+":53")
			}
		}
	} else {
		setAuthoritativeDNSServers()
	}
}

func query(domain string, server string) (record panAnalyticRecord) {
	msg := &dns.Msg{}
	msg.SetQuestion(dns.Fqdn(domain), dns.TypeA)
	in, err := dns.Exchange(msg, server)
	if err == nil {
		if len(in.Answer) > 0 {
			record.Domain = domain
			record.Ttl = in.Answer[0].Header().Ttl
			switch firstAnswer := in.Answer[0].(type) {
			case *dns.CNAME:
				record.Type = "CNAME"
				record.Target = TrimSuffixPoint(firstAnswer.Target)
			case *dns.A:
				record.Type = "A"
				for _, ans := range in.Answer {
					if a, ok := ans.(*dns.A); ok {
						record.IP = append(record.IP, a.A.String())
					}
				}
			}
		}
	}

	return record
}

// AnalyzePanAnalytic 分析泛解析
func AnalyzePanAnalytic() {
	hash := md5.New()
	hash.Write([]byte(rootDomain))
	domain := hex.EncodeToString(hash.Sum(nil)) + "." + rootDomain
	cnames := map[string]struct{}{}
	ipLists := map[string]struct{}{}

	// 获取权威 DNS 服务器
	setAuthoritativeDNSServers()

	ch := make(chan panAnalyticRecord)
	for _, server := range authoritativeDNSServers {
		for i := 0; i < 5; i++ {
			go func(server string) {
				ch <- query(domain, server)
			}(server)
		}
	}
	for _ = range authoritativeDNSServers {
		for i := 0; i < 5; i++ {
			pRecord := <-ch
			switch pRecord.Type {
			case "CNAME":
				// TODO cname 泛解析的情况下，是否把 IP 也加入黑名单
				cnames[pRecord.Target] = struct{}{}
				panAnalyticRecords[pRecord.Target] = pRecord.Ttl

			case "A":
				for _, ip := range pRecord.IP {
					ipLists[ip] = struct{}{}
					panAnalyticRecords[ip] = pRecord.Ttl
				}
			}
		}
	}
	close(ch)

	go func() {
		for cname := range cnames {
			chPanAnalyticRecord <- DNSRecord{domain, "CNAME", cname, []string{}}
		}
		if len(ipLists) > 0 {
			IP := []string{}
			for ip := range ipLists {
				IP = append(IP, ip)
			}
			chPanAnalyticRecord <- DNSRecord{domain, "A", "", IP}
		}
		close(chPanAnalyticRecord)
	}()
	log.Debugf("pan analytic record: %v\n", panAnalyticRecords)
}

// IsPanAnalytic 是否为泛解析域名
func IsPanAnalytic(record string, ttl uint32) bool {
	_ttl, ok := panAnalyticRecords[TrimSuffixPoint(record)]
	return ok && _ttl == ttl
}
