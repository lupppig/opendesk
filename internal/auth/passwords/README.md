# Breached-password denylist

`breached.txt` holds every password 12 to 128 characters long from two lists of
passwords seen in breaches:

- the UK National Cyber Security Centre's 100,000 most commonly breached passwords
  (published from the Have I Been Pwned corpus), and
- the top 1,000,000 of the Pwdb breach compilation.

Anything shorter is already refused by the length rule, so carrying it here would
only make the file bigger. That leaves 30,399 entries (ASVS 2.1.7 asks for at least
the top 1,000 that match the password rules).

Entries are lowercased, deduplicated and sorted. The comparison in
`auth.CheckPassword` lowercases the candidate, so the casing here is not a
weakening: `Password123456` and `password123456` are both refused.

Sources: https://github.com/danielmiessler/SecLists
(`Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt` and
`Passwords/Common-Credentials/Pwdb_top-1000000.txt`)

Refreshing it is a matter of re-running that filter (12..128 characters, printable,
lowercased) and committing the result. Nothing else needs to change.
