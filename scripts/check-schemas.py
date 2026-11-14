#!/usr/bin/env python3
"""Valide les fichiers de configuration du dépôt contre leur JSON Schema (utilisé en CI)."""
import json
import sys

import jsonschema
import yaml

main = json.load(open("config/apis.schema.json"))
fragment = json.load(open("config/apis.fragment.schema.json"))
files = {
    "config/apis.example.yml": main,
    "config/apis.demo.yml": main,
    "deploy/kubernetes/base/apis.yml": main,
    "config/apis.d/equipe-crm.yml": fragment,
    "deploy/compose/config/apis.yml": main,
    "deploy/compose/config/apis.d/paiement.yml": fragment,
}
failed = False
for path, schema in files.items():
    errors = list(jsonschema.Draft7Validator(schema).iter_errors(yaml.safe_load(open(path))))
    for e in errors:
        print(f"{path}: {'/'.join(map(str, e.path))}: {e.message}")
    failed |= bool(errors)
    if not errors:
        print(f"{path}: ok")
sys.exit(1 if failed else 0)
