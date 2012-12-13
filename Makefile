# pre-deploy hook for using the fork's import
switch-author:
	find . -name "*.go" -print | xargs -n 1 sed -i -e "s/garyburd\/gopkgdoc/srid\/gopkgdoc/g"
	find . -name "*-e" | xargs rm -f
