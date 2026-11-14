-- v0.6 : libellés des changements en français (text reste le texte anglais d'oasdiff)
ALTER TABLE change_items ADD COLUMN text_fr TEXT;
-- Avant la v0.6, les éléments produits par Kansabooj lui-même (et non par oasdiff) étaient rédigés en français
-- dans text : on les recopie, la vue anglaise de ces anciens éléments reste en français.
UPDATE change_items SET text_fr = text
 WHERE rule_id IN ('api-version-changed', 'non-semantic-change', 'first-capture', 'capture-mode-changed',
                   'html-content-changed', 'html-line-added', 'html-line-removed');
