<p align="center">
  <img src="docs/design/logo/agentnook.svg" alt="agentnook — ein kleiner Agent in seiner eigenen Ecke" width="160">
</p>

<h1 align="center">agentnook</h1>

<p align="center"><strong>Eine eigene Ecke für deinen Coding-Agenten.</strong></p>

**Eigener Platz, separater Kernel:** agentnook führt **Pi**, **OpenCode v2** oder **Claude Code** in einer eigenen, kurzlebigen [smolvm](https://github.com/smol-machines/smolvm)-VM aus – mit gezielten Ordnerfreigaben, persistenten Profilen und Logins. Die CLI heißt `nook`.

## Start

Voraussetzungen für die Nutzung: macOS mit Apple Silicon oder Linux mit KVM und eine funktionierende Installation von **smolvm ab 1.33.0** im `PATH`. Docker, Node.js und die Agenten müssen auf dem Host **nicht** installiert sein. Für den ersten Image-Aufbau ist Internetzugriff auf Container-Registry und Paketquellen nötig; Build und Ausführung erfolgen lokal.

Die folgenden Befehle bauen `nook` aus dem Quellcode. Dafür wird [mise](https://mise.jdx.dev/) benötigt; ein bereits gebautes `nook`-Binary benötigt mise und Go nicht.

```sh
# Im agentnook-Repository:
mise trust
mise install
mise run build
export PATH="$PWD/bin:$PATH"

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
nook watch                              # laufende Agenten dieses Projektordners beobachten
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
nook images build claude-pinned --agent claude --agent-version 2.1.99
nook --agent claude --image claude-dev
nook images default claude-dev           # Standard nur für Claude Code
nook images remove claude-alt
```

Image-Namen sind unveränderlich und agentenübergreifend eindeutig. Images eines anderen Agententyps werden beim Start abgelehnt. Entfernen ist nur möglich, wenn weder ein Agentenstandard noch eine gespeicherte Auswahl das Image verwendet – auch inaktive Agentenauswahlen zählen. Profile und smolvm-Caches bleiben unberührt.

Images entstehen ausschließlich mit smolvm: Basisimage laden → Build-VM starten → Werkzeuge und Agent installieren → VM stoppen → lokal packen → Agentenversion in einer frischen VM prüfen. Erst danach wird das Image im Katalog registriert. Die Build-VM wird anschließend gelöscht; fehlgeschlagene Builds verändern den Katalog nicht.

Build-VMs erhalten keine Projekt-/Profilfreigaben, SSH-Agenten oder Agenten-Zugangsdaten. Gebaut wird für die native Host-Architektur auf einer Debian-basierten Node-24-Basis. Gespeichert wird nur das `.smolmachine`-Artefakt, kein zusätzliches Runtime-Binary. Spätere Starts verwenden dieses lokale Artefakt ohne erneute Installation. npm-Installationsskripte werden gezielt für das gewählte Agentenpaket freigegeben (`--allow-scripts=PAKET`), nicht pauschal für alle Abhängigkeiten. OpenCode benötigt sein `postinstall`-Skript, um das passende Linux-Binary einzurichten. Automatische Updates von Claude Code und OpenCode sind deaktiviert; Updates erfolgen über neue Builds.

Jeder Start erstellt eine frische benannte Arbeits-VM, führt den Agenten darin aus und löscht die VM beim regulären Ende sowie nach SIGINT/SIGTERM/SIGHUP. Dadurch bleibt sie während der Nutzung für `nook watch` sichtbar. Nach einem harten Prozessabbruch (SIGKILL/Stromausfall) kann eine `nook-run-*`- oder `nook-build-*`-Maschine zurückbleiben; sie lässt sich gezielt mit `smolvm machine delete --name NAME --force` entfernen.

### Profile und Daten

`AGENTNOOK_HOME` überschreibt den Standardpfad `~/.agentnook/`:

```text
~/.agentnook/
  config.toml
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

**Grenzen der Isolation:** Projektordner und ausgewähltes Agenten-HOME sind standardmäßig beschreibbar. Änderungen dort und an zusätzlichen beschreibbaren Freigaben bleiben auf dem Host erhalten; die VM selbst wird beim Beenden verworfen. Ein Agent kann auf alle Daten und Logins seines ausgewählten Profils zugreifen. Profile deshalb nur zwischen vertrauenswürdigen Projekten teilen. Die VM hat Netzwerkzugriff; agentnook hebt interne Permission-Dialoge der Agenten nicht auf.

Das Konfigurationsformat verlangt `version = 1` und Imagepfade mit `.smolmachine`. Alte `.tar`-Images, alte Konfigurationen und `--pi-*`-Flags werden **nicht migriert oder unterstützt**. Bestehende Daten werden nicht gelöscht.

Beim Wechsel von Docker-Builds `config.toml` zunächst sichern und aus dem aktiven Pfad nehmen (z. B. in `config.pre-smolvm.toml` umbenennen). Danach Images neu aufbauen und dieselben Profilnamen verwenden: `profiles/` bleibt unverändert, bestehende Logins werden wiederverwendet. Projektzuordnungen und Ressourcen müssen erneut gewählt werden. Alte Image-Dateien bleiben unangetastet.

Für einen vollständig getrennten Neustart einen neuen Datenpfad verwenden:

```sh
AGENTNOOK_HOME="$HOME/.agentnook-multi" nook
```

## Fleet im Blick

`nook watch` zeigt laufende agentnook-Instanzen des aktuellen Projektordners und Datenpfads – unabhängig vom Agententyp. Die breite Ansicht enthält Agent, Profil, VM-Status, Aktivität, Laufzeit, CPU-/RAM-Limits und Imageversion; Details zeigen sämtliche Freigaben. In schmalen Terminals stehen Agent und Version im Detailbereich.

Mit ↑/↓ eine Instanz auswählen. `q` beendet nur die Ansicht, nicht die VMs. Fehlende oder veraltete Telemetrie erscheint als `unknown`. `VERSION¹` stammt aus dem Imagekatalog, nicht aus einer erneuten Abfrage des Gasts. Arbeits-VMs tragen den exakten lokalen Artefaktpfad als smolvm-Label. Darüber wird die Imageversion zugeordnet; geänderte Projektstandards verändern die Anzeige bereits laufender Instanzen nicht.

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

## Entwicklung

```sh
mise run check                          # Tests, Prozess-E2E, Vet und Build
```

### Architekturgrenzen

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

Die Gastadapter-Tests benötigen Node.js ab 22.19; ohne Node werden sie übersprungen. Prozess-E2E verwendet ausschließlich smolvm-Stubs und Testzugangsdaten, weder Docker noch einen Hypervisor. Echte VM-Tests für Pi und OpenCode sind separat opt-in und benötigen smolvm ab 1.33.0, einen funktionierenden Hypervisor und Internet für den lokalen Erstaufbau:

```sh
mise run test:vm
```

## Logo

Das Quadrat in der Ecke steht für einen Agenten mit eigenem Platz. Basis: [Tabler Icons — box-align-bottom-left (filled)](https://github.com/tabler/tabler-icons/blob/main/icons/filled/box-align-bottom-left.svg), MIT-lizenziert. Lizenznachweise: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
