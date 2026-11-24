# agentnook: Benutzung

[Zurück zur Übersicht](../README.md)

## Start

Voraussetzungen für die Nutzung: macOS mit Apple Silicon oder Linux mit KVM und eine funktionierende Installation von **smolvm ab 1.36.1** im `PATH`. Docker, Node.js und die Agenten müssen auf dem Host **nicht** installiert sein. Für den ersten Image-Aufbau ist Internetzugriff auf Container-Registry und Paketquellen nötig; Build und Ausführung erfolgen lokal.

### Release installieren (empfohlen)

Der Installer lädt das veröffentlichte Binary für macOS (Apple Silicon) oder Linux (AMD64/ARM64) und prüft die SHA-256-Prüfsumme. Kein GitHub-Login, kein Go oder mise nötig.

```sh
curl -qfsS https://raw.githubusercontent.com/flobilosaurus/agentnook/main/install.sh | sh
```

Der Einzeiler führt `install.sh` aus `main` aus und installiert das neueste stabile Release. Für eine feste Version ersetze `| sh` durch z. B. `| sh -s -- v0.1.0`. Zum Prüfen des Skripts vor der Ausführung kannst du `| sh` weglassen. `smolvm` wird nicht mitinstalliert.

### Alternativ: aus dem Quellcode installieren

Dafür wird [mise](https://mise.jdx.dev/) benötigt; das installierte Binary benötigt mise und Go nicht.

```sh
# Im agentnook-Repository:
mise trust
mise install
mise run install
```

Beide Methoden installieren oder aktualisieren `nook` ohne `sudo` in `~/.local/bin`. Für einen anderen absoluten Ordner im `PATH` setze `NOOK_INSTALL_DIR`, z. B. `NOOK_INSTALL_DIR=/absoluter/pfad/bin mise run install`. Shell-Konfigurationen werden nicht verändert.

Falls `~/.local/bin` noch nicht im `PATH` der aktuellen Shell steht, ergänze ihn mit `export PATH="$HOME/.local/bin:$PATH"` (Bash/Zsh) oder `$env.PATH = ($env.PATH | prepend ($env.HOME | path join ".local" "bin"))` (Nushell).

```sh
# Anschließend im gewünschten Projektordner:
cd /pfad/zum/projekt
nook
```

**Einmal einrichten, danach nur `nook`.** Beim ersten Start in einem Projektordner den üblichen Agenten wählen und ein Profil anlegen oder wiederverwenden. Gibt es noch kein Profil, ist `default` bereits vorgeschlagen; Enter genügt. Das passende Image wird automatisch lokal gebaut und ausgewählt. Eine Imageauswahl oder Ressourcenabfrage ist im normalen Einrichtungsdialog nicht nötig. Standardressourcen: 2 CPUs, 2 GiB RAM, 20 GiB Gast-Disk.

Im Agenten einmal anmelden: Pi über `/login`, OpenCode über `/connect`, Claude Code über seinen Login-Dialog beziehungsweise `/login`. Die Anmeldung erfolgt **innerhalb der VM**. Host-Konfigurationen und Host-Logins werden nicht übernommen; auf dem Host muss kein Agent installiert sein.

## Benutzung

```sh
nook                                    # einmal einrichten; danach gespeicherten Start nutzen
nook -- --continue                      # Argumente an den gespeicherten Agenten weiterreichen
nook watch                              # alle Projekte im aktuellen AGENTNOOK_HOME beobachten
nook --mount ../lib:/mnt/lib:ro          # zusätzliche Freigabe speichern, nur lesbar
nook --clear-mounts                      # zusätzliche Freigaben entfernen
nook --select                           # nur bei Bedarf die Einrichtung ändern
nook --help
```

Agent, Profil und Image werden im **exakten Projektordner** wiederverwendet, ohne erneut nachzufragen oder das Image neu zu bauen. Neue Image-Standards ändern eine bestehende Einrichtung nicht. In einem neuen Projektordner erfolgt die Einrichtung einmal erneut; vorhandene Profile und Images lassen sich dabei weiterverwenden.

Mehrere `--mount`-Flags ersetzen gemeinsam die gespeicherte Zusatzliste; ohne `:ro` sind Freigaben beschreibbar. Argumente nach `--` werden unverändert weitergegeben und nicht gespeichert.

### Erweiterte Optionen — nur bei Bedarf

`nook --help` zeigt den normalen Ablauf. Sämtliche bisherigen Flags bleiben verfügbar und stehen unter `nook --help-all`. Agenten- und Imagewechsel sind keine Voraussetzung für die tägliche Nutzung:

```sh
nook --help-all
nook --select                           # Agent/Profil ändern; Imageverwaltung im Untermenü
nook --select-image                     # nur ein anderes Image wählen
nook --agent claude --profile arbeit    # Agent und Profil ausdrücklich vorgeben
nook --agent claude --image claude-dev  # bestimmtes Image desselben Agenten wählen
nook --cpus 4 --mem 4096                 # Ressourcen der gespeicherten Auswahl ändern
nook --dry-run                          # Startplan als JSON, ohne Änderungen
```

Das flexible Datenmodell bleibt erhalten: Ein Profil kann mehrere Agenten mit getrennten HOMEs enthalten. Profil, Image, Ressourcen und Zusatzfreigaben werden pro **Projektordner + Agent** gespeichert. Ein ausdrücklicher Wechsel zu Claude überschreibt die gespeicherte Pi-Auswahl nicht; `nook` verwendet anschließend den zuletzt gewählten Agenten.

`--agent-version VERSION` kann beim ersten automatischen Build eine exakte Version vorgeben; bei vorhandenen Images muss sie der eingebauten Version entsprechen. Versionswechsel benötigen einen neuen Image-Build.

Ohne Terminal beziehungsweise mit `AGENTNOOK_NONINTERACTIVE=1` sind keine Auswahldialoge möglich. Bereits eingerichtete Ordner starten auch dann mit `nook`; für die erstmalige Einrichtung beide Werte explizit angeben:

```sh
nook --agent claude --profile arbeit -- --print "Erkläre dieses Projekt"
```

### Images verwalten

Jedes Image enthält genau einen Agenten. Versionen werden beim Build exakt festgehalten und mit dessen `--version` geprüft. Ohne `--agent-version` wird die aktuelle stabile npm-Version aufgelöst. OpenCode verwendet das v2-Paket `@opencode/cli`, nicht das v1-Paket `opencode-ai`.

```sh
nook images
nook images build pi-dev --agent pi
nook images build opencode-dev --agent opencode
nook images build claude-dev --agent claude --set-default
nook images build claude-pinned --agent claude --agent-version 2.1.323
nook --agent claude --image claude-dev
nook images default claude-dev           # Standard nur für Claude Code
nook images remove claude-alt
```

Image-Namen sind unveränderlich und agentenübergreifend eindeutig. Images eines anderen Agententyps werden beim Start abgelehnt. Entfernen ist nur möglich, wenn weder ein Agentenstandard noch eine gespeicherte Auswahl das Image verwendet – auch inaktive Agentenauswahlen zählen. Profile und smolvm-Caches bleiben unberührt.

Images entstehen ausschließlich mit smolvm: Basisimage laden → Build-VM starten → Werkzeuge und Agent installieren → VM stoppen → lokal packen → Agentenversion in einer frischen VM prüfen. Erst danach wird das Image im Katalog registriert. Die Build-VM wird anschließend gelöscht; fehlgeschlagene Builds verändern den Katalog nicht.

Build-VMs erhalten keine Projekt-/Profilfreigaben, SSH-Agenten oder Agenten-Zugangsdaten. Gebaut wird für die native Host-Architektur auf einer Debian-basierten Node-24-Basis. Gespeichert wird nur das `.smolmachine`-Artefakt, kein zusätzliches Runtime-Binary. Spätere Starts verwenden dieses lokale Artefakt ohne erneute Installation. npm-Installationsskripte werden gezielt für das gewählte Agentenpaket freigegeben (`--allow-scripts=PAKET`), nicht pauschal für alle Abhängigkeiten. OpenCode benötigt sein `postinstall`-Skript, um das passende Linux-Binary einzurichten. Automatische Updates von Claude Code und OpenCode sind deaktiviert; Updates erfolgen über neue Builds.

Jeder Start erstellt eine frische benannte Arbeits-VM, führt den Agenten darin aus und löscht die VM beim regulären Ende sowie nach SIGINT/SIGTERM/SIGHUP. Dadurch bleibt sie während der Nutzung für `nook watch` sichtbar. Nach einem harten Prozessabbruch (SIGKILL/Stromausfall) kann eine `nook-run-*`- oder `nook-build-*`-Maschine zurückbleiben; sie lässt sich gezielt mit `smolvm machine delete --name NAME --force` entfernen.

### Netzwerkverbindungen freigeben

**Alle Arbeits-VMs blockieren standardmäßig Host-Loopback, LAN, private IPv4-/IPv6-Netze, Tailscale/CGNAT und Cloud-Metadaten** über smolvms `--block-private`. Öffentliche Internetziele bleiben erreichbar. Der Schutz gilt auch für bereits gespeicherte Projektzuordnungen beim nächsten Start; laufende VMs müssen neu gestartet werden. Mindestversion ist **smolvm 1.36.1**, einschließlich der neueren Gateway-/Loopback-Schutzfixes. `--network full` bedeutet nur „keine interaktiven TCP-Abfragen“, nicht „Host/LAN freigeben“.

Zusätzlich lässt sich eine interaktive TCP-Freigabe für erreichbare Ziele aktivieren:

```sh
# Terminal 1, im Projekt:
nook --network ask                      # Modus für diesen Projektordner + Agent speichern

# Terminal 2, beliebiger Ordner, gleicher AGENTNOOK_HOME:
nook network                            # Historie; Tab wechselt zu offenen Freigaben

# Spätere Starts verwenden den gespeicherten Modus:
nook
nook --network full                     # ohne TCP-Abfragen; Host/LAN bleibt gesperrt
```

Gezielte private Ausnahmen sind nur ohne interaktive TCP-Abfragen möglich:

```sh
nook --network full --allow-private 10.20.0.0/16 --allow-private fd00:1234::/48
nook                                   # Ausnahmen für diesen Projektordner + Agent wiederverwenden
nook --clear-allow-private              # alle gespeicherten privaten Ausnahmen entfernen
nook --network ask --clear-allow-private # sicher zu interaktiven Freigaben wechseln
```

Mehrere `--allow-private`-Flags ersetzen gemeinsam die gespeicherte Ausnahmeliste. CIDRs werden kanonisiert und doppelte Einträge entfernt. Ohne diese Flags wird die bestehende Liste wiederverwendet. Jede Ausnahme öffnet **alle Ports und Transporte** im genannten Netz; Cloud-Metadaten bleiben gesperrt. Deshalb nur enge, benötigte Netze oder einzelne IPs (`/32`, `/128`) freigeben. Eine Ausnahme für das Gateway allein öffnet dessen Host-Loopback-Ziel nicht: Beim Standard-Gateway `100.96.0.1` benötigt Hostzugriff ausdrücklich sowohl `100.96.0.1/32` als auch `127.0.0.1/32`. smolvm-interne Dienste und hostverwaltetes DNS bleiben ausgenommen.

**`ask` und private Ausnahmen dürfen nicht kombiniert werden.** smolvms statische `--allow-private`-Regeln würden sonst den Freigabe-Broker umgehen und auch UDP/ICMP öffnen. Das wird vor Build oder VM-Start abgelehnt, einschließlich gespeicherter Ausnahmen. Ausnahmen zuerst mit `--clear-allow-private` entfernen oder ausdrücklich `--network full` verwenden.

`nook network` öffnet die dauerhafte VM-Historie; `Tab` wechselt zu offenen Anfragen aller vermittelten VM-Sitzungen im aktuellen Datenpfad und zurück. Im Freigabe-Tab zuerst mit ↑/↓ oder `j`/`k` eine Anfrage auswählen, dann `a` für **einmal erlauben**, `s` für **Ziel-IP und Port für diese VM-Sitzung erlauben** oder `d` für **ablehnen**. Auswahl springt bei Ablauf einer Anfrage nicht automatisch auf eine andere. `q` schließt nur die Ansicht. Das Agenten-Terminal bleibt unberührt. Ohne offene Ansicht gibt es keine automatischen Freigaben.

Entscheidung muss innerhalb von **12 Sekunden** erfolgen; smolvm setzt eine Obergrenze von 15 Sekunden. Timeout, fehlender Broker oder ungültige Kommunikation sperren die Verbindung. Nach Timeout muss das Gastprogramm die Verbindung erneut versuchen; eigene Client-Timeouts können früher greifen. Sitzungsfreigaben gelten nur für die attestierte VM-Startidentität und die exakte Ziel-IP samt Port. Andere Zieladressen, Ports und VM-Starts benötigen neue Freigaben. Parallele offene Anfragen desselben VM/IP/Port-Ziels werden durch eine Sitzungsfreigabe gemeinsam erlaubt. Freigaberegeln werden nicht dauerhaft gespeichert; verfügbare Netzwerkentscheidungen werden dagegen in der unten beschriebenen Historie archiviert. Bereits erlaubte offene Verbindungen werden durch Schließen der Ansicht oder Broker-Ausfall nicht getrennt. Build-VMs bleiben unverändert und benötigen weiterhin Internetzugriff.

**Sicherheitsgrenzen:** Freigegeben werden TCP-Verbindungen, nicht einzelne HTTP-Requests. Eine erlaubte Verbindung kann viele Requests, mehrere Hostnamen oder Tunnelverkehr tragen. Eine IP-/Port-Sitzungsfreigabe gilt auch für andere Dienste auf derselben gemeinsamen IP und demselben Port. Optionale TLS-SNI-Namen stammen vom Gast und sind nur ungeprüfte Anzeigehinweise, keine Freigabeschlüssel. HTTPS wird nicht entschlüsselt; URLs, Methoden und Inhalte werden nicht geprüft. DNS bleibt durch smolvm verwaltet und wird nicht interaktiv freigegeben; darüber ist weiterhin Datenabfluss möglich. Sonstiges ausgehendes UDP/ICMP wird ohne zusätzliche smolvm-Ausnahmen gesperrt. smolvm-interne Dienste bleiben ausgenommen; dessen eigene Schutzregeln können Ziele auch nach Freigabe weiterhin blockieren. Dies ist keine vollständige Data-Loss-Prevention.

Broker läuft ausschließlich auf dem Host, mit getrennten zufälligen Tokens für smolvm und Freigabeoberfläche. Der Datenpfad muss dem aktuellen Host-Benutzer gehören und darf nicht für Gruppe oder andere Benutzer beschreibbar sein. Nur das smolvm-Startkommando erhält den Mediation-Token; Gast, Agentenargumente und Konfiguration erhalten ihn nicht. Unter `AGENTNOOK_HOME/network/` liegen kurzlebige, nur für den Host-Benutzer lesbare Registrierungen mit lokalen Kontrolladressen und Kontrolltokens; sie werden nicht in VMs eingebunden. Gast-Payloads und Agenten-Zugangsdaten werden dort nicht gespeichert. Beim regulären Ende werden Broker und Registrierung entfernt. Nach SIGKILL können wirkungslose Registrierungen zurückbleiben; die Ansicht ignoriert nicht erreichbare Broker. Bei mehr als 256 Registrierungen müssen veraltete Dateien bei gestopptem `nook` entfernt werden.

`--dry-run --network ask` zeigt den vermittelten Startplan, ohne Broker, Tokens oder Konfigurationsänderungen anzulegen. Alte Konfigurationen ohne Netzwerkmodus behalten Zugriff auf öffentliche Ziele ohne TCP-Abfragen, erhalten aber ebenfalls Host-/LAN-Schutz. `ask` wird nie still auf `full` zurückgestuft. Nicht unterstützte smolvm-Versionen werden vor Einrichtung oder Build abgelehnt. Die Freigabeansicht benötigt ein Terminal, die VM selbst kann auch ohne Terminal laufen und bleibt dann auf Freigaben aus einer separaten Ansicht angewiesen.

### Dauerhafte Netzwerkhistorie

Für jede **neu gestartete Arbeits-VM** werden die von smolvm bereitgestellten ausgehenden Netzwerkereignisse auf dem Host gespeichert, unabhängig davon, ob `nook network` geöffnet ist. Die Historie bleibt nach VM-Ende und VM-Löschung erhalten. Build-VMs bleiben ausgenommen; frühere Starts werden nicht nachträglich rekonstruiert.

Jedes Ereignis enthält Zeitpunkt, VM-Startidentität soweit verfügbar, Protokoll, Ziel-IP/Port beziehungsweise DNS-Namen, **`ALLOWED` (erlaubt)** oder **`DENIED` (verboten)** sowie smolvms Entscheidungsgrund. `ALLOWED` bedeutet nur durch die Netzwerkregel zugelassen, nicht erfolgreiche TCP-Verbindung, erfolgreiche HTTP-Antwort oder sicheren Inhalt. Wiederholte Verbindungsversuche und Datagramme können mehrere Einträge erzeugen.

**Abdeckung bei unverändertem Netzwerkverhalten:**

- **`ask`:** strukturierte Engine-Entscheidungen einschließlich automatischer Sitzungsfreigaben, Broker-Ablehnungen/-Ausfall und Sperren vor dem Broker. DNS-, UDP- und ICMP-Ereignisse werden übernommen, soweit smolvm sie meldet. smolvms Grund `broker_decision` unterscheidet nicht zwischen jeder einzelnen Benutzeraktion und einem Broker-Timeout.
- **`full`:** nur vorhandene **Sperrereignisse**. smolvm 1.36.1 stellt für diesen Modus keine vollständige Historie erlaubter Ziele bereit. Diese Lücke wird pro VM ausdrücklich als `denials-only` angezeigt; eine leere Historie ist kein Nachweis für fehlenden Verkehr. Für die Protokollierung werden weder zusätzliche Abfragen noch neue ICMP-/UDP-Sperren eingeführt.

```sh
nook network                            # VMs mit Historie, auch beendete
nook network --machine nook-run-…       # bestimmte VM zunächst auswählen
nook network --json                     # ohne Terminal, laufende VM oder smolvm-Installation
nook network --json --machine nook-run-… --limit 100
nook network --json --machine nook-run-… --before BYTE_CURSOR --limit 100
```

Im Historie-Tab wählen ↑/↓ oder `j`/`k` die VM; `PgUp` zeigt ältere, `PgDn` neuere Ereignisse und `Home` wieder die neuesten. Freigabetasten wirken ausschließlich im Freigabe-Tab. JSON liefert unter `history.page` die Ereignisse, `machine`, `next_before` und `has_older`; mit diesem Byte-Cursor sind auch große Archive seitenweise lesbar. Maximal 200 Ereignisse pro JSON-Seite; die TUI passt die Seitengröße an ihre Höhe an und benötigt mindestens 48 Spalten × 22 Zeilen. Archive bleiben auch bei nicht erreichbaren Live-Brokern lesbar; Probleme der Live-Ansicht werden separat ausgewiesen.

Speicherort: `${AGENTNOOK_HOME:-~/.agentnook}/network/history/<VM-NAME>/machine.json` und `events.jsonl`. Verzeichnisse sind nur für den Host-Benutzer zugänglich, Dateien haben Modus `0600` und werden nicht in Gäste eingebunden. Gespeichert werden ausschließlich freigegebene Metadatenfelder, keine OAuth-URLs, Authorization-Codes, Header, Tokens oder Request-Payloads. DNS-Namen und Zieladressen können trotzdem vertraulich sein. Eine automatische Löschung oder Aufbewahrungsgrenze gibt es nicht; entsprechend kann das Archiv wachsen.

Der Collector übernimmt smolvms Host-Auditdateien laufend und leert sie beim regulären Ende **nach VM-Stopp, vor VM-Löschung**. Bereits geöffnete Quelldateien bleiben bei Rotation lesbar. Erkannte Rotationslücken, ungültige Ereignisse, Speicherfehler oder nicht bestätigte Stopps werden als unvollständige Historie markiert. SIGKILL, Stromausfall und extreme Ereignisraten können weiterhin Lücken verursachen; eine fehlende Abschlusszeit heißt „nicht finalisiert“, nicht zwingend „läuft noch“. Gastinterner Loopback und eingehende Host→VM-Verbindungen werden nicht vollständig erfasst. Mehrere HTTP-Requests über dieselbe TCP-Verbindung erzeugen keine separaten Request-Einträge. Auch das ist kein vollständiger Paketmitschnitt und keine HTTPS-Request-Inspektion.

### Profile und Daten

`AGENTNOOK_HOME` überschreibt den Standardpfad `~/.agentnook/`:

```text
~/.agentnook/
  config.toml
  network/
    history/
      nook-run-…/
        machine.json  # VM-Zuordnung, Abdeckung, Abschluss und Warnungen
        events.jsonl  # dauerhafte Netzwerkentscheidungen
  images/
    pi-default-…/
      image.smolmachine
  profiles/
    arbeit/
      pi/          # Guest-HOME, enthält .pi/
      opencode/    # Guest-HOME, enthält .config/, .local/share/, …
      claude/      # Guest-HOME, enthält .claude/ und .claude.json
```

Nur das HOME des gewählten Agenten wird nach `/root` eingebunden. Dadurch bleiben Konfiguration, Zugangsdaten, Sessions und Cache persistent; andere Profile und Agenten-HOMEs werden nicht freigegeben. Die Agenten verwalten ihre Dateien selbst. Zusatzfreigaben dürfen weder dieses HOME noch Telemetrie- und Systempfade überlagern.

**Grenzen der Isolation:** Projektordner und ausgewähltes Agenten-HOME sind standardmäßig beschreibbar. Änderungen dort und an zusätzlichen beschreibbaren Freigaben bleiben auf dem Host erhalten; die VM selbst wird beim Beenden verworfen. Ein Agent kann auf alle Daten und Logins seines ausgewählten Profils zugreifen. Profile deshalb nur zwischen vertrauenswürdigen Projekten teilen. Die VM erreicht standardmäßig öffentliche Internetziele, nicht aber Host/LAN; private Ausnahmen müssen ausdrücklich freigegeben werden. `--network ask` vermittelt ausgehende TCP-Verbindungen mit den oben beschriebenen Grenzen. agentnook hebt interne Permission-Dialoge der Agenten nicht auf.

Das Konfigurationsformat verlangt `version = 1` und Imagepfade mit `.smolmachine`. Alte `.tar`-Images, alte Konfigurationen und `--pi-*`-Flags werden **nicht migriert oder unterstützt**. Bestehende Daten werden nicht gelöscht.

Beim Wechsel von Docker-Builds `config.toml` zunächst sichern und aus dem aktiven Pfad nehmen (z. B. in `config.pre-smolvm.toml` umbenennen). Danach Images neu aufbauen und dieselben Profilnamen verwenden: `profiles/` bleibt unverändert, bestehende Logins werden wiederverwendet. Projektzuordnungen und Ressourcen müssen erneut gewählt werden. Alte Image-Dateien bleiben unangetastet.

Für einen vollständig getrennten Neustart einen neuen Datenpfad verwenden:

```sh
AGENTNOOK_HOME="$HOME/.agentnook-multi" nook
```

## Fleet im Blick

`nook watch` zeigt immer laufende agentnook-Instanzen aller Projektordner im aktuellen `AGENTNOOK_HOME` – unabhängig vom Agententyp und Aufrufordner. Instanzen anderer Datenpfade bleiben ausgeschlossen. Ein projektlokaler Modus und das Flag `--all` existieren nicht mehr. Links steht ein einfacher, immer vollständig geöffneter Navigator: **Profil → Projekt → Worktree → VM**. Nur VM-Zeilen sind auswählbar; Profil-, Projekt- und Worktree-Zeilen dienen als Überschriften und werden übersprungen. Rechts folgt der Inspektor der gewählten VM und zeigt die Instanzen ihres exakten Workspace/Profils sowie Details einschließlich vollständiger Pfade, CPU-/RAM-Limits, Imageversion und sämtlicher Freigaben. In schmalen Terminals steht der Navigator oberhalb des Inspektors. Es gibt keine klappbaren Gruppen oder Detail-Tabs.

↑/↓ oder `j`/`k` wählt die vorherige/nächste VM im Baum – auch über Projekt- und Profilgrenzen hinweg. `Tab`/`Shift+Tab` wechselt zwischen Navigator und Instanzliste, ohne die gemeinsame VM-Auswahl zu ändern. ←/`h` fokussiert den Navigator, →/`l` die Instanzen. `PgUp`/`PgDn` scrollt Details, `Home`/`End` springt darin zum Anfang/Ende. Die Auswahl bleibt bei Aktualisierungen erhalten; endet eine gewählte VM, wird möglichst eine andere Instanz desselben Workspace/Profils gewählt, sonst eine VM desselben Projekts oder Profils und zuletzt eine andere verfügbare VM. Gruppen werden dabei nie markiert. `q` beendet nur die Ansicht, nicht die VMs.

Git-Metadaten ordnen Worktrees auch außerhalb des Hauptordners ihrem Projekt zu. Hauptcheckouts bleiben als Gruppenknoten sichtbar, wenn nur Worktree-Instanzen aktiv sind. Das gleiche Repository unter verschiedenen Profilen bleibt getrennt; Unterordner eines Checkouts behalten eigene Workspace-Gruppen. Die Erkennung ist rein lesend, zeitlich begrenzt und kurz zwischengespeichert. Ohne Git oder bei Erkennungsfehlern bleiben Instanzen als exakte Ordnergruppen sichtbar. Startverhalten und Projektfilter ändern sich dadurch nicht.

Fehlende oder veraltete Telemetrie erscheint als `unknown`. `Version¹` stammt aus dem Imagekatalog, nicht aus einer erneuten Abfrage des Gasts. Arbeits-VMs tragen den exakten lokalen Artefaktpfad als smolvm-Label. Darüber wird die Imageversion zugeordnet; geänderte Projektstandards verändern die Anzeige bereits laufender Instanzen nicht.

Interaktive Telemetrie nutzt je Agent einen eigenen Adapter:

- **Pi:** temporär geladene Extension mit Lifecycle-Ereignissen und Heartbeat.
- **OpenCode v2:** temporäres, nur lesbar geladenes System-Plugin für Session-, Permission- und Form-Ereignisse. Aktivität wird für Sessions am aktuellen Projektort zusammengefasst; die Laufzeit gehört zum OpenCode-Serverprozess.
- **Claude Code:** temporäre, rein beobachtende Hooks plus Supervisor. Hooks speichern keine Prompts oder Tool-Inputs und treffen keine Permission-Entscheidungen. Der Supervisor hält Heartbeat und Laufzeit über die Lebensdauer des gestarteten Agenten aufrecht; die Aktivität folgt den zuletzt beobachteten Hauptsession-Ereignissen.

Print-/Utility-Aufrufe erhalten keine Telemetrie. Eigene Claude-`--settings`-Argumente deaktivieren die automatische Anbindung für diesen Aufruf, damit sie unverändert bleiben. Deaktivierte Hooks/Plugins können die Aktivitätserkennung einschränken. Telemetrie bleibt untrusted und dient ausschließlich der Anzeige, nicht der Steuerung oder Freigabe von Host-Aktionen.

## Herdr-Integration

Bei interaktiven Starts innerhalb eines Herdr-Panes leitet agentnook die gemeinsame Telemetrie automatisch weiter: `idle`, `working`, `blocked` oder `unknown`. Voraussetzung sind `herdr` im Host-`PATH` und dessen Pane-Kontext; Pi benötigt für `blocked` kooperierende Extensions. Herdr-Socket und Pane-ID bleiben auf dem Host.

```sh
AGENTNOOK_HERDR=0 nook                   # nur Herdr-Weiterleitung deaktivieren
```

Fleet und Telemetrie funktionieren auch ohne Herdr.
