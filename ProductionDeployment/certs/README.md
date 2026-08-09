# Intermediate CA Certificates

These intermediate certificates are bundled into the Docker image because some
South African government and utility websites serve incomplete TLS certificate
chains (leaf certificate only, missing intermediates). Go's TLS client and most
non-browser HTTP clients cannot verify such chains without the intermediates.

Browsers work around this using AIA (Authority Information Access) fetching and
intermediate certificate caching, but server-side clients do not.

## Included Certificates

| File | Issuer | Used By | Expires |
|------|--------|---------|---------|
| `sectigo-dv.crt` | USERTrust RSA CA | durban.gov.za | 2030-12-31 |
| `sectigo-public-server-auth-dv-r36.crt` | Sectigo Public Server Authentication Root R46 | durban.gov.za | 2036-03-21 |
| `thawte-g1.crt` | DigiCert Global Root G2 | randwater.co.za | 2027-11-02 |
| `entrust-ov.crt` | Sectigo Public Server Auth Root R46 | joburg.org.za | 2027-12-10 |
| `emsign-ssl-g1.crt` | emSign Root CA - G1 | pppkenya.go.ke | 2033-02-18 |

## Sources

Downloaded from official CA AIA endpoints:
- http://crt.sectigo.com/SectigoRSADomainValidationSecureServerCA.crt
- http://crt.sectigo.com/SectigoPublicServerAuthenticationCADVR36.crt
- http://cacerts.thawte.com/ThawteTLSRSACAG1.crt
- http://crt.sectigo.com/EntrustOVTLSIssuingRSACA2.crt
- https://repository.emsign.com/certs/emSignSSLCAG1.crt
