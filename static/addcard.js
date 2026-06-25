// Add Card Page JavaScript
// Handles card reader input and API integration for creating new card masks

document.addEventListener('DOMContentLoaded', function() {
    const cardInput = document.getElementById('cardInput');
    const mesaInput = document.getElementById('mesaInput');
    const loading = document.getElementById('loading');
    const errorMessage = document.getElementById('errorMessage');
    const statusDisplay = document.getElementById('statusDisplay');
    const currentMesaDisplay = document.getElementById('currentMesaDisplay');

    // Keep focus on the card input field at all times (kiosk mode)
    function keepFocus() {
        cardInput.focus();
    }

    // Keep focus on card input field - never let it go to mesa input
    document.addEventListener('click', function(e) {
        if (e.target !== cardInput && e.target !== mesaInput) {
            keepFocus();
        }
    });

    // Prevent tab key from moving focus to mesa input
    mesaInput.addEventListener('keydown', function(e) {
        if (e.key === 'Tab') {
            e.preventDefault();
            keepFocus();
        }
    });

    // Handle card input
    cardInput.addEventListener('keydown', function(e) {
        console.log('Key pressed:', e.key);
        if (e.key === 'Enter') {
            const cardNumber = cardInput.value.trim();
            console.log('Enter pressed, card number:', cardNumber);
            if (cardNumber) {
                addCardMask(cardNumber);
            }
        }
    });

    // Also handle input changes for card readers that don't send Enter key
    let inputTimeout;
    cardInput.addEventListener('input', function() {
        clearTimeout(inputTimeout);
        inputTimeout = setTimeout(function() {
            const cardNumber = cardInput.value.trim();
            if (cardNumber && cardNumber.length > 3) {
                addCardMask(cardNumber);
            }
        }, 500); // Wait 500ms after last input before processing
    });

    // Handle mesa input changes to update display
    mesaInput.addEventListener('input', function() {
        const mesaNumber = mesaInput.value.trim();
        if (mesaNumber) {
            currentMesaDisplay.textContent = mesaNumber;
        } else {
            currentMesaDisplay.textContent = '-';
        }
    });

    // Initial focus
    keepFocus();

    // Function to add card mask
    async function addCardMask(cardNumber) {
        console.log('addCardMask called with:', cardNumber);
        
        // Get mesa number from input
        let mesaNumber = mesaInput.value.trim();
        
        if (!mesaNumber) {
            showError('Please enter a starting Mesa number');
            cardInput.value = '';
            keepFocus();
            return;
        }

        // Reset display
        hideError();
        showLoading();
        hideResults();

        try {
            const apiUrl = `/api/addcard?cardnum=${encodeURIComponent(cardNumber)}&mesanum=${encodeURIComponent(mesaNumber)}`;
            console.log('Fetching from:', apiUrl);
            const response = await fetch(apiUrl);
            console.log('Response status:', response.status);
            
            if (!response.ok) {
                throw new Error(`HTTP error! status: ${response.status}`);
            }

            const data = await response.json();

            console.log('Response data:', data);
            if (data.error) {
                console.log('Error in response:', data.error);
                showError(data.error);
                cardInput.value = '';
                keepFocus();
                return;
            }

            // Display success message
            console.log('Displaying card mask creation result');
            displaySuccess(data);
            
            // Increment mesa number for next card
            const nextMesaNumber = parseInt(mesaNumber) + 1;
            mesaInput.value = nextMesaNumber;
            currentMesaDisplay.textContent = nextMesaNumber;
            
            // Clear card input for next card
            cardInput.value = '';
            
        } catch (error) {
            console.error('Error adding card mask:', error);
            showError('Failed to add card mask. Please try again.');
            cardInput.value = '';
        } finally {
            hideLoading();
            // Keep focus on input for next card
            setTimeout(keepFocus, 100);
        }
    }

    // Display success message
    function displaySuccess(data) {
        statusDisplay.classList.remove('error');
        statusDisplay.classList.add('success', 'active');
        
        document.getElementById('statusMessage').textContent = 'Card Mask Created Successfully!';
        document.getElementById('cardNumber').textContent = data.cardNumber || '-';
        document.getElementById('mesaNumber').textContent = data.mesaNumber || '-';
        document.getElementById('maskName').textContent = data.maskName || '-';
    }

    // UI Helper functions
    function showLoading() {
        loading.classList.add('active');
    }

    function hideLoading() {
        loading.classList.remove('active');
    }

    function showError(message) {
        errorMessage.textContent = message;
        errorMessage.classList.add('active');
    }

    function hideError() {
        errorMessage.classList.remove('active');
    }

    function hideResults() {
        statusDisplay.classList.remove('active', 'success', 'error');
    }
});