# Teams Plugin for Relicta

Official Teams plugin for [Relicta](https://github.com/relicta-tech/relicta) - Send release notifications to Microsoft Teams.

## Installation

```bash
relicta plugin install teams
relicta plugin enable teams
```

## Configuration

Add to your `release.config.yaml`:

```yaml
plugins:
  - name: teams
    enabled: true
    config:
      # Add configuration options here
```

## License

MIT License - see [LICENSE](LICENSE) for details.
