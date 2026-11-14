-- v0.9 : auteur d'un check manuel (identité SSO, token:<nom>, anonymous ou cli), pour le journal
ALTER TABLE checks ADD COLUMN actor TEXT;
