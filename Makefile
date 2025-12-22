BINARY_NAME := blgen

.PHONY: build
build:
	go build -o $(BINARY_NAME) .
	@echo "Built $(BINARY_NAME) executable."

.PHONY: run
run: build
	./$(BINARY_NAME)

.PHONY: clean
clean:
	rm -f $(BINARY_NAME)
	# Clean the default output file
	rm -f BlockedCountriesBlocks.txt
