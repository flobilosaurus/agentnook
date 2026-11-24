# agentnook: Entwicklung

[Zurück zur Übersicht](../README.md)

## Lokale Checks

```sh
mise run check                          # Formatprüfung, Tests, Prozess-E2E, Vet und Build
mise run fmt                            # Go-Dateien formatieren
mise run fmt:check                      # Formatierung nur prüfen, nichts ändern
```

## GitHub CI und Releases

[GitHub Actions](../.github/workflows/ci.yml) führt bei Branch-Pushes und Pull Requests sowie bei `v*`-Tag-Pushes die Formatprüfung, Race-Tests einschließlich Architekturtests, Prozess-E2E, `go vet`, Abhängigkeitsprüfung und Build aus. Geprüft wird auf Linux AMD64/ARM64 und macOS ARM64; Go und Node stammen aus `.mise.toml`. Nach erfolgreichen Checks führt der Job `vm` bei Pushes (einschließlich Versionstags), Pull Requests und manuellen Workflow-Starts zusätzlich die echten VM-Tests für Pi, OpenCode und Claude Code auf einem frischen GitHub-gehosteten Ubuntu-24.04-AMD64-Runner mit KVM aus. Ein eigener Rechner oder Self-hosted Runner ist nicht nötig.

Ein gepushter Versionstag veröffentlicht **erst nach erfolgreichen Checks auf allen Plattformen und erfolgreichen echten VM-Tests** ein GitHub Release:

```sh
git tag v1.2.3
git push origin v1.2.3
```

Release-Tags verwenden `vMAJOR.MINOR.PATCH`, optional mit Prerelease-/Build-Suffix. Tags wie `v1.2.3-rc.1` werden als GitHub-Prerelease markiert. Bereits vorhandene Tags lösen keinen nachträglichen Release-Build aus; veröffentlicht wird beim Tag-Push.

Assets: `nook_<TAG>_darwin_arm64.tar.gz`, `nook_<TAG>_linux_amd64.tar.gz`, `nook_<TAG>_linux_arm64.tar.gz` sowie `checksums.txt` (SHA256). Archive enthalten `nook`, README und Drittlizenznachweise; `nook --version` zeigt den Tag. Release-Notizen werden automatisch erzeugt. VM-Images werden weiterhin lokal gebaut und nicht veröffentlicht.

Veröffentlichung nutzt den eingebauten `GITHUB_TOKEN`; kein zusätzliches Secret nötig. Schreibrechte auf Repository-Inhalte erhält nur der Release-Job.

## Echte VM-Tests auf GitHub-Runnern

Der Job `vm` nutzt `ubuntu-24.04` (AMD64). Vor dem Testlauf prüft er `/dev/kvm`, gibt dem aktuellen Runner-Benutzer Zugriff und überprüft mit `KVM_CREATE_VM`, dass eine VM tatsächlich angelegt werden kann. Fehlendes oder unbenutzbares KVM macht den Job rot; echte VM-Tests werden nicht still übersprungen.

Go wird über mise eingerichtet. Der Workflow lädt das vollständige **smolvm-1.36.1-Linux-x86_64-Bundle** in ein temporäres Job-Verzeichnis, prüft dessen fest hinterlegte SHA256 und verwendet den enthaltenen Wrapper samt Bibliotheken, Gast-Rootfs und Disk-Templates. Anschließend führt `mise run test:vm` alle drei Agenten-VM-Tests sowie echte Netzwerkfreigabe- und Host-/LAN-Schutztests aus. Die Agententests bauen ihre Images frisch auf und nutzen temporäre Profile. Die Netzwerktests verwenden separate minimale Linux-Gäste ohne Agenteninstallation und lokale Testserver. Sie prüfen Ablehnung, Einzel- und Sitzungsfreigabe sowie Broker-Ausfall, einschließlich ausbleibender Serverkontakte vor Freigabe. Der Schutztest prüft Host-Sperren in `full` und `ask`, IPv4-gemappte IPv6-Ziele, die Gateway-/Loopback-Trennung und explizite private Ausnahmen. Zusätzlich prüfen die Tests die echte Archivierung erlaubter und verbotener TCP-/DNS-Entscheidungen, Sperren vor dem Broker und den Erhalt der Historie nach VM-Löschung. Alle Tests benötigen keine Agenten-Zugangsdaten oder zusätzlichen Repository-Secrets. Der Testlauf hat ein Limit von 25 Minuten, der gesamte VM-Job von 35 Minuten.

Jeder Job erhält einen frischen GitHub-Runner; eine Runner-Registrierung entfällt. Benötigt wird Internetzugriff auf GitHub Releases, Docker Hub und die Debian-/npm-Paketquellen. Das smolvm-Bundle wird nach dem Job entfernt; GitHub verwirft anschließend den Runner. Fork-PRs können abhängig von den Repository-Einstellungen eine Workflow-Freigabe benötigen.

**Abdeckungsgrenze:** Echte VM-Tests laufen in CI nur auf Linux AMD64. Die regulären macOS-ARM64- und Linux-ARM64-Checks bleiben erhalten, testen aber nicht deren echte VM-Backends. KVM ist auf den verwendeten Linux-Runnern verfügbar; GitHub garantiert allgemeine Nested-VM-Nutzung jedoch nicht. Bei einem Infrastrukturproblem schlägt der VM-Job fehl und blockiert Tag-Releases.

## Architekturgrenzen

Agentenspezifische Implementierungen liegen unter `internal/agents/pi/`, `opencode/` und `claude/`, einschließlich ihrer eingebetteten Gastskripte. `internal/agents/agent.go` definiert ausschließlich den gemeinsamen Vertrag und Hilfsfunktionen. Nur `internal/agents/registry/registry.go` importiert und verdrahtet konkrete Adapter; die CLI reicht sie an VM-, Image-, Fleet- und Telemetriecode weiter.

`internal/architecture/architecture_test.go` sichert diese Trennung mit **arch-go** und ergänzenden AST-/Dateistrukturtests ab:

- Erlaubte Paketabhängigkeiten sind explizit festgelegt; Infrastruktur darf keine Registry oder konkreten Adapter importieren.
- Adapter dürfen keine anderen Adapter oder Host-Infrastruktur importieren.
- Konkrete Agenten-IDs, Pfade und Protokolldetails dürfen nicht in gemeinsame Implementierungen zurückwandern.
- Jeder Adapter besitzt und bettet seine eigenen Gastskripte ein; gemeinsam bleibt nur der generische JSON-Reporter.
- Negativkontrollen prüfen, dass unerlaubte Abhängigkeiten und Fallunterscheidungen tatsächlich erkannt werden.

```sh
mise run test:architecture
```

Architekturtests laufen auch über `mise run test` und `mise run check`. Ein neuer Agent benötigt einen Adapter, einen Registry-Eintrag und die Erweiterung der expliziten Architekturregeln. Adapter liefern deklarative Telemetriepläne; nur der gemeinsame Launcher darf daraus private Host-Dateien und Mounts erstellen. Zusätzliche geschützte Gastpfade werden vom Adapter geliefert und beim Start geprüft.

Die Gastadapter-Tests benötigen Node.js ab 22.19; ohne Node werden sie übersprungen. Prozess-E2E verwendet ausschließlich smolvm-Stubs und Testzugangsdaten, weder Docker noch einen Hypervisor. Echte VM-Tests für Pi, OpenCode und Claude Code laufen in CI im separaten GitHub-gehosteten Linux-KVM-Job. Lokal bleiben sie opt-in und benötigen smolvm ab 1.36.1, einen funktionierenden Hypervisor und Internet für den lokalen Erstaufbau:

```sh
mise run test:vm

# Nur echte Netzwerkfreigaben und Host-Schutz prüfen (smolvm >= 1.36.1):
AGENTNOOK_VM_SMOKE=1 go test -count=1 -run '^TestReal(Private)?NetworkVM$' -timeout 3m -v ./test/e2e
```

## Manuelle Testsession mit vier VMs

`scripts/testing-session.py` startet im **aktuellen Herdr-Tab** vier Pi-Instanzen mit den Profilen `private` und `work`, dazu `nook watch` und `nook network`. Das funktioniert in **Herdr Desktop** (`herdr-app`) und in der **Herdr TUI** (`herdr`). Das ursprüngliche Panel bleibt als Steuerterminal erhalten; andere vorhandene Panels werden nicht ersetzt:

```text
┌──────────────────────────────────────────────────────┐
│ ursprüngliches Terminal: up / status / down           │
├─────────────┬─────────────┬─────────────┬──────────────┤
│ private-1   │ private-2   │             │              │
├─────────────┼─────────────┤ nook watch  │ nook network │
│ work-1      │ work-2      │             │              │
└─────────────┴─────────────┴─────────────┴──────────────┘
```

Voraussetzungen: Python **3.11+**, `herdr-app` bzw. `herdr` im `PATH`, smolvm ab 1.36.1 und ein vorhandenes Pi-Standardimage. `nook` wird aus dem `PATH`, ersatzweise aus `bin/nook` dieses Repositories verwendet. Bei Bedarf zuerst `mise run build` und `bin/nook images build pi-testing --agent pi --set-default` ausführen.

In Herdr Desktop `up` in einem lokalen Terminal ausführen, das im gewünschten Tab als Panel angezeigt wird. Das Script findet es über `HC_TERMINAL_ID`; `HERDR_ENV=1` wird nur in der TUI benötigt. Bei abweichendem Desktop-Konfigurationspfad `HERDR_APP_HOME` setzen. Falls etwa `agentenv` ein anderes `HOME` setzt, `AGENTNOOK_HOME` explizit auf das Datenverzeichnis mit dem gewünschten Pi-Standardimage setzen. Für `down` denselben Wert verwenden.

```sh
# Im Repository, im gewünschten Herdr-Panel:
./scripts/testing-session.py up
./scripts/testing-session.py status
./scripts/testing-session.py down

# Optional: bestimmtes Binary oder keine interaktiven TCP-Freigaben
./scripts/testing-session.py up --nook "$PWD/bin/nook" --network full
```

Die Session nutzt das aktuelle `${AGENTNOOK_HOME:-~/.agentnook}` und legt Profile beim VM-Start bei Bedarf an. Vorhandene Logins dieser Profile werden wiederverwendet; fehlen sie, im jeweiligen Agenten anmelden. Zwei VMs desselben Profils teilen dessen persistentes Agenten-HOME. Jede VM erhält einen eigenen, wiederverwendbaren Testordner unter `${AGENTNOOK_HOME}-testing-projects/` (standardmäßig `~/.agentnook-testing-projects/`), 2 CPUs und 2048 MiB RAM; insgesamt also 8 GiB Gast-RAM plus Host-Overhead. Die Projektordner liegen neben dem Datenverzeichnis, weil `nook` überlappende Pfade ablehnt. Nur die Testordner werden als Projekte eingebunden, nicht das Repository.

Standardmäßig gilt `--network ask`, ohne private Netzwerkausnahmen. In `nook network` mit `Tab` zu offenen Freigaben wechseln. Beide Beobachter zeigen auch andere Instanzen desselben Datenpfads. `status` zeigt gestartete beziehungsweise beendete Panel-Prozesse; ob alle vier VMs tatsächlich laufen, zeigt `nook watch`.

`down` fordert die eigenen Prozesse zum Beenden auf, wartet auf nook-VM-Cleanup und schließt anschließend ausschließlich die sechs erstellten Panels. Profile, Logins, Testdateien und Netzwerkhistorie bleiben bestehen. Ein zweites `up` wird bis zum erfolgreichen `down` abgelehnt. Bei unterbrochenem Start oder Cleanup-Timeout bleibt der Zustand erhalten: Panels prüfen und `down` erneut ausführen, optional mit `--timeout 180`. Kein pauschales VM-Löschen, kein SIGKILL. Bei hart getöteten Supervisoren kann manuelle Bereinigung nötig sein; das Script schließt dann nicht blind die Panels. Für `status` und `down` dasselbe `AGENTNOOK_HOME` und dieselbe Herdr-Instanz verwenden. In Herdr Desktop muss das Fenster für die Panel-Bereinigung geöffnet sein.

Script-Tests ohne Herdr-Steuerung oder echte VMs:

```sh
python3 -m unittest discover -s scripts -p 'test_*.py'
```
